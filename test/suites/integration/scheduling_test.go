package integration_test

import (
	"fmt"

	"github.com/aws/karpenter/pkg/apis/awsnodetemplate/v1alpha1"
	"github.com/aws/karpenter/pkg/apis/provisioning/v1alpha5"
	awsv1alpha1 "github.com/aws/karpenter/pkg/cloudprovider/aws/apis/v1alpha1"
	"github.com/aws/karpenter/pkg/test"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

var _ = Describe("Scheduling", func() {
	It("should support well known labels", func() {
		provider := test.AWSNodeTemplate(v1alpha1.AWSNodeTemplateSpec{AWS: awsv1alpha1.AWS{
			SecurityGroupSelector: map[string]string{"karpenter.sh/discovery": env.ClusterName},
			SubnetSelector:        map[string]string{"karpenter.sh/discovery": env.ClusterName},
		}})
		provisioner := test.Provisioner(test.ProvisionerOptions{ObjectMeta: metav1.ObjectMeta{Name: provider.Name}, ProviderRef: &v1alpha5.ProviderRef{Name: provider.Name}})
		nodeSelector := map[string]string{
			// Well Known
			v1.LabelTopologyRegion:           env.Region,
			v1.LabelTopologyZone:             fmt.Sprintf("%sa", env.Region),
			v1.LabelInstanceTypeStable:       "g4dn.8xlarge",
			v1.LabelOSStable:                 "linux",
			v1.LabelArchStable:               "amd64",
			v1alpha5.LabelCapacityType:       "on-demand",
			v1alpha5.ProvisionerNameLabelKey: provisioner.Name,
			// Well Known to AWS
			awsv1alpha1.LabelInstanceHypervisor:      "nitro",
			awsv1alpha1.LabelInstanceCategory:        "g",
			awsv1alpha1.LabelInstanceGeneration:      "4",
			awsv1alpha1.LabelInstanceFamily:          "g4dn",
			awsv1alpha1.LabelInstanceSize:            "8xlarge",
			awsv1alpha1.LabelInstanceCPU:             "32",
			awsv1alpha1.LabelInstanceMemory:          "131072",
			awsv1alpha1.LabelInstancePods:            "58", // May vary w/ environment
			awsv1alpha1.LabelInstanceGPUName:         "t4",
			awsv1alpha1.LabelInstanceGPUManufacturer: "nvidia",
			awsv1alpha1.LabelInstanceGPUCount:        "1",
			awsv1alpha1.LabelInstanceGPUMemory:       "16384",
			awsv1alpha1.LabelInstanceLocalNVME:       "900",
			// Deprecated Labels
			v1.LabelFailureDomainBetaZone:   fmt.Sprintf("%sa", env.Region),
			v1.LabelFailureDomainBetaRegion: env.Region,
			"beta.kubernetes.io/arch":       "amd64",
			"beta.kubernetes.io/os":         "linux",
			v1.LabelInstanceType:            "g4dn.8xlarge",
		}
		requirements := lo.MapToSlice(nodeSelector, func(key string, value string) v1.NodeSelectorRequirement {
			return v1.NodeSelectorRequirement{Key: key, Operator: v1.NodeSelectorOpIn, Values: []string{value}}
		})
		deployment := test.Deployment(test.DeploymentOptions{Replicas: 1, PodOptions: test.PodOptions{
			NodeSelector: nodeSelector,
			NodePreferences: requirements,
			NodeRequirements: requirements,
		}})
		// Ensure that we're exercising all well known labels
		Expect(lo.Keys(nodeSelector)).To(ContainElements(append(v1alpha5.WellKnownLabels.UnsortedList(), lo.Keys(v1alpha5.NormalizedLabels)...)))
		env.ExpectCreated(provisioner, provider, deployment)
		env.EventuallyExpectHealthyPodCount(labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels), int(*deployment.Spec.Replicas))
		env.ExpectCreatedNodeCount("==", 1)
	})
	It("should provision a node for naked pods", func() {
		provider := test.AWSNodeTemplate(v1alpha1.AWSNodeTemplateSpec{AWS: awsv1alpha1.AWS{
			SecurityGroupSelector: map[string]string{"karpenter.sh/discovery": env.ClusterName},
			SubnetSelector:        map[string]string{"karpenter.sh/discovery": env.ClusterName},
		}})
		provisioner := test.Provisioner(test.ProvisionerOptions{ProviderRef: &v1alpha5.ProviderRef{Name: provider.Name}})
		pod := test.Pod()

		env.ExpectCreated(provisioner, provider, pod)
		env.EventuallyExpectHealthy(pod)
		env.ExpectCreatedNodeCount("==", 1)
	})
	It("should provision a node for a deployment", func() {
		provider := test.AWSNodeTemplate(v1alpha1.AWSNodeTemplateSpec{AWS: awsv1alpha1.AWS{
			SecurityGroupSelector: map[string]string{"karpenter.sh/discovery": env.ClusterName},
			SubnetSelector:        map[string]string{"karpenter.sh/discovery": env.ClusterName},
		}})
		provisioner := test.Provisioner(test.ProvisionerOptions{ProviderRef: &v1alpha5.ProviderRef{Name: provider.Name}})

		deployment := test.Deployment(test.DeploymentOptions{Replicas: 50})
		env.ExpectCreated(provisioner, provider, deployment)
		env.EventuallyExpectHealthyPodCount(labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels), int(*deployment.Spec.Replicas))
		env.ExpectCreatedNodeCount("<=", 2) // should probably all land on a single node, but at worst two depending on batching
	})
	It("should provision a node for a self-affinity deployment", func() {
		provider := test.AWSNodeTemplate(v1alpha1.AWSNodeTemplateSpec{AWS: awsv1alpha1.AWS{
			SecurityGroupSelector: map[string]string{"karpenter.sh/discovery": env.ClusterName},
			SubnetSelector:        map[string]string{"karpenter.sh/discovery": env.ClusterName},
		}})
		provisioner := test.Provisioner(test.ProvisionerOptions{ProviderRef: &v1alpha5.ProviderRef{Name: provider.Name}})
		// just two pods as they all need to land on the same node
		podLabels := map[string]string{"test": "self-affinity"}
		deployment := test.Deployment(test.DeploymentOptions{
			Replicas: 2,
			PodOptions: test.PodOptions{
				ObjectMeta: metav1.ObjectMeta{
					Labels: podLabels,
				},
				PodRequirements: []v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{MatchLabels: podLabels},
						TopologyKey:   v1.LabelHostname,
					},
				},
			},
		})

		env.ExpectCreated(provisioner, provider, deployment)
		env.EventuallyExpectHealthyPodCount(labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels), 2)
		env.ExpectCreatedNodeCount("==", 1)
	})
	It("should provision three nodes for a zonal topology spread", func() {
		provider := test.AWSNodeTemplate(v1alpha1.AWSNodeTemplateSpec{AWS: awsv1alpha1.AWS{
			SecurityGroupSelector: map[string]string{"karpenter.sh/discovery": env.ClusterName},
			SubnetSelector:        map[string]string{"karpenter.sh/discovery": env.ClusterName},
		}})
		provisioner := test.Provisioner(test.ProvisionerOptions{ProviderRef: &v1alpha5.ProviderRef{Name: provider.Name}})

		// one pod per zone
		podLabels := map[string]string{"test": "zonal-spread"}
		deployment := test.Deployment(test.DeploymentOptions{
			Replicas: 3,
			PodOptions: test.PodOptions{
				ObjectMeta: metav1.ObjectMeta{
					Labels: podLabels,
				},
				TopologySpreadConstraints: []v1.TopologySpreadConstraint{
					{
						MaxSkew:           1,
						TopologyKey:       v1.LabelTopologyZone,
						WhenUnsatisfiable: v1.DoNotSchedule,
						LabelSelector:     &metav1.LabelSelector{MatchLabels: podLabels},
					},
				},
			},
		})

		env.ExpectCreated(provisioner, provider, deployment)
		env.EventuallyExpectHealthyPodCount(labels.SelectorFromSet(podLabels), 3)
		env.ExpectCreatedNodeCount("==", 3)
	})
})
