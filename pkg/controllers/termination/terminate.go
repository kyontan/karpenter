/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package termination

import (
	"context"
	"errors"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/util/clock"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"knative.dev/pkg/logging"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/aws/karpenter/pkg/apis/provisioning/v1alpha5"
	"github.com/aws/karpenter/pkg/cloudprovider"
	"github.com/aws/karpenter/pkg/scheduling"
	"github.com/aws/karpenter/pkg/utils/functional"
	podutil "github.com/aws/karpenter/pkg/utils/pod"
	"github.com/aws/karpenter/pkg/utils/ptr"
)

type Terminator struct {
	EvictionQueue *EvictionQueue
	KubeClient    client.Client
	CoreV1Client  corev1.CoreV1Interface
	CloudProvider cloudprovider.CloudProvider
	Clock         clock.Clock
}

type NodeDrainErr error

func IsNodeDrainErr(err error) bool {
	var nodeDrainErr NodeDrainErr
	return errors.As(err, &nodeDrainErr)
}

// cordon cordons a node
func (t *Terminator) cordon(ctx context.Context, node *v1.Node) error {
	// 1. Check if node is already cordoned
	if node.Spec.Unschedulable {
		return nil
	}
	// 2. Cordon node
	persisted := node.DeepCopy()
	node.Spec.Unschedulable = true
	if err := t.KubeClient.Patch(ctx, node, client.MergeFrom(persisted)); err != nil {
		return fmt.Errorf("patching node %s, %w", node.Name, err)
	}
	logging.FromContext(ctx).Infof("Cordoned node")
	return nil
}

// drain evicts pods from the node and returns true when all pods are evicted
// https://kubernetes.io/docs/concepts/architecture/nodes/#graceful-node-shutdown
func (t *Terminator) drain(ctx context.Context, node *v1.Node) (bool, error) {
	// Get evictable pods
	pods, err := t.getPods(ctx, node)
	if err != nil {
		return false, fmt.Errorf("listing pods for node, %w", err)
	}
	var podsToEvict []*v1.Pod
	// Skip node due to pods that are not able to be evicted
	for _, p := range pods {
		// if a pod doesn't have owner references then we can't expect a controller to manage its lifecycle
		if len(p.ObjectMeta.OwnerReferences) == 0 {
			return false, NodeDrainErr(fmt.Errorf("pod %s/%s does not have any owner references", p.Namespace, p.Name))
		} else if podutil.HasDoNotEvict(p) {
			return false, NodeDrainErr(fmt.Errorf("pod %s/%s has do-not-evict annotation", p.Namespace, p.Name))
		}
		// Ignore if unschedulable is tolerated, since they will reschedule
		if (scheduling.Taints{{Key: v1.TaintNodeUnschedulable, Effect: v1.TaintEffectNoSchedule}}).Tolerates(p) == nil {
			continue
		}
		// Ignore static mirror pods
		if podutil.IsOwnedByNode(p) {
			continue
		}
		podsToEvict = append(podsToEvict, p)
	}
	// Enqueue for eviction
	t.evict(podsToEvict)
	return len(podsToEvict) == 0, nil
}

// terminate calls cloud provider delete then removes the finalizer to delete the node
func (t *Terminator) terminate(ctx context.Context, node *v1.Node) error {
	// 1. Delete the instance associated with node
	if err := t.CloudProvider.Delete(ctx, node); err != nil {
		return fmt.Errorf("terminating cloudprovider instance, %w", err)
	}
	// 2. Remove finalizer from node in APIServer
	persisted := node.DeepCopy()
	node.Finalizers = functional.StringSliceWithout(node.Finalizers, v1alpha5.TerminationFinalizer)
	if err := t.KubeClient.Patch(ctx, node, client.MergeFrom(persisted)); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("removing finalizer from node, %w", err)
	}
	logging.FromContext(ctx).Infof("Deleted node")
	return nil
}

// getPods returns a list of evictable pods for the node
func (t *Terminator) getPods(ctx context.Context, node *v1.Node) ([]*v1.Pod, error) {
	podList := &v1.PodList{}
	if err := t.KubeClient.List(ctx, podList, client.MatchingFields{"spec.nodeName": node.Name}); err != nil {
		return nil, fmt.Errorf("listing pods on node, %w", err)
	}
	var pods []*v1.Pod
	for _, p := range podList.Items {
		// Ignore if the pod is complete and doesn't need to be evicted
		if podutil.IsTerminal(ptr.Pod(p)) {
			continue
		}
		// Ignore if kubelet is partitioned and pods are beyond graceful termination window
		if t.isStuckTerminating(ptr.Pod(p)) {
			continue
		}
		pods = append(pods, ptr.Pod(p))
	}
	return pods, nil
}

func (t *Terminator) evict(pods []*v1.Pod) {
	// 1. Prioritize noncritical pods https://kubernetes.io/docs/concepts/architecture/nodes/#graceful-node-shutdown
	critical := []*v1.Pod{}
	nonCritical := []*v1.Pod{}
	for _, pod := range pods {
		if !pod.DeletionTimestamp.IsZero() {
			continue
		}
		if pod.Spec.PriorityClassName == "system-cluster-critical" || pod.Spec.PriorityClassName == "system-node-critical" {
			critical = append(critical, pod)
		} else {
			nonCritical = append(nonCritical, pod)
		}
	}
	// 2. Evict critical pods if all noncritical are evicted
	if len(nonCritical) == 0 {
		t.EvictionQueue.Add(critical)
	} else {
		t.EvictionQueue.Add(nonCritical)
	}
}

func (t *Terminator) isStuckTerminating(pod *v1.Pod) bool {
	if pod.DeletionTimestamp == nil {
		return false
	}
	return t.Clock.Now().After(pod.DeletionTimestamp.Time.Add(1 * time.Minute))
}
