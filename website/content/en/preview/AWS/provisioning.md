---
title: "Provisioning Configuration"
linkTitle: "Provisioning"
weight: 10
description: >
  Learn AWS-specific Karpenter provisioning settings
---

Provisioner settings specific to Karpenter for the AWS cloud provider are described here.
The following example shows optional and required settings for a Karpenter provisioner for AWS:

```yaml
apiVersion: karpenter.sh/v1alpha5
kind: Provisioner
metadata:
  name: default
spec:
  requirements:
    - key: karpenter.sh/capacity-type         # optional, set to on-demand by default, spot if both are listed
      operator: In
      values: ["spot"]
  limits:
    resources:
      cpu: 1000                               # optional, recommended to limit total provisioned CPUs
      memory: 1000Gi
  providerRef:                                # optional, recommended to use instead of `provider`
    name: default
  ttlSecondsAfterEmpty: 30                    # optional, but never scales down if not set
  ttlSecondsUntilExpired: 2592000             # optional, but never expires if not set
---
apiVersion: karpenter.k8s.aws/v1alpha1
kind: AWSNodeTemplate
metadata:
  name: default
spec:
  subnetSelector:                             # required
    karpenter.sh/discovery: ${CLUSTER_NAME}
  securityGroupSelector:                      # required, when not using launchTemplate
    karpenter.sh/discovery: ${CLUSTER_NAME}
  instanceProfile: MyInstanceProfile          # optional, if already set in controller args
  launchTemplate: MyLaunchTemplate            # optional, see Launch Template documentation
  tags:
    InternalAccountingTag: "1234"             # optional, add tags for your own use

```
Refer to [Provisioner API]({{<ref "../provisioner.md" >}}) for settings that are not specific to AWS.
See below for other AWS provider-specific parameters.

## spec.providerRef

The ProviderRef is a reference to the AWSNodeTemplate resource that contains all the parameters needed by the AWS Cloud Provider.
You can review these fields [in the code](https://github.com/aws/karpenter/blob{{< githubRelRef >}}pkg/apis/awsnodetemplate/v1alpha1/awsnodetemplate.go).

### InstanceProfile
An `InstanceProfile` is a way to pass a single IAM role to an EC2 instance. Karpenter will not create one automatically.
A default profile may be specified on the controller, allowing it to be omitted here. If not specified as either a default
or on the controller, node provisioning will fail. The KarpenterControllerPolicy will also need to have permissions for
`iam:PassRole` to the role provided here or provisioning will fail.

```
spec:
  provider:
    instanceProfile: MyInstanceProfile
```

### LaunchTemplate

A launch template is a set of configuration values sufficient for launching an EC2 instance (e.g., AMI, storage spec).

A custom launch template is specified by name. If none is specified, Karpenter will automatically create a launch template.

Review the [Launch Template documentation](../launch-templates/) to learn how to create a custom one.

```
spec:
  provider:
    launchTemplate: MyLaunchTemplate
```

### SubnetSelector (required)

Karpenter discovers subnets using [AWS tags](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/Using_Tags.html).

Subnets may be specified by any AWS tag, including `Name`. Selecting tag values using wildcards (`*`) is supported.

Subnet IDs may be specified by using the key `aws-ids` and then passing the IDs as a comma-separated string value.

When launching nodes, Karpenter automatically chooses a subnet that matches the desired zone. If multiple subnets exist for a zone, the one with the most available IP addresses will be used.

**Examples**

Select all subnets with a specified tag:
```
  subnetSelector:
    karpenter.sh/discovery/MyClusterName: '*'
```

Select subnets by name:
```
  subnetSelector:
    Name: my-subnet
```

Select subnets by an arbitrary AWS tag key/value pair:
```
  subnetSelector:
    MySubnetTag: value
```

Select subnets using wildcards:
```
  subnetSelector:
    Name: "*Public*"

```

Specify subnets explicitly by ID:
```yaml
  subnetSelector:
    aws-ids: "subnet-09fa4a0a8f233a921,subnet-0471ca205b8a129ae"
```

### SecurityGroupSelector (required, when not using launchTemplate)

The security group of an instance is comparable to a set of firewall rules.

EKS creates at least two security groups by default, [review the documentation](https://docs.aws.amazon.com/eks/latest/userguide/sec-group-reqs.html) for more info.

Security groups may be specified by any AWS tag, including "Name". Selecting tags using wildcards (`*`) is supported.

{{% alert title="Note" color="primary" %}}
When launching nodes, Karpenter uses all of the security groups that match the selector. If multiple security groups with the tag `karpenter.sh/discovery/MyClusterName` match the selector, this may result in failures using the AWS Load Balancer controller. The Load Balancer controller only supports a single security group having that tag key. See this [issue](https://github.com/kubernetes-sigs/aws-load-balancer-controller/issues/2367) for more details.
{{% /alert %}}

To verify if this restriction affects you, run the following commands.
```bash
CLUSTER_VPC_ID="$(aws eks describe-cluster --name $CLUSTER_NAME --query cluster.resourcesVpcConfig.vpcId --output text)"

aws ec2 describe-security-groups --filters Name=vpc-id,Values=$CLUSTER_VPC_ID Name=tag-key,Values=karpenter.sh/discovery/$CLUSTER_NAME --query 'SecurityGroups[].[GroupName]' --output text
```

If multiple securityGroups are printed, you will need a more targeted securityGroupSelector.

**Examples**

Select all security groups with a specified tag:
```
spec:
  provider:
    securityGroupSelector:
      karpenter.sh/discovery/MyClusterName: '*'
```

Select security groups by name, or another tag (all criteria must match):
```
 securityGroupSelector:
   Name: my-security-group
   MySecurityTag: '' # matches all resources with the tag
```

Select security groups by name using a wildcard:
```
 securityGroupSelector:
   Name: "*Public*"
```

Specify security groups explicitly by ID:
```yaml
 securityGroupSelector:
   aws-ids: "sg-063d7acfb4b06c82c,sg-06e0cf9c198874591"
```

### Tags

Karpenter adds tags to all resources it creates, including EC2 Instances, EBS volumes, and Launch Templates. The default set of AWS tags are listed below.

```
Name: karpenter.sh/provisioner-name/<provisioner-name>
karpenter.sh/provisioner-name: <provisioner-name>
kubernetes.io/cluster/<cluster-name>: owned
```

Additional tags can be added in the provider tags section which are merged with and can override the default tag values.
```
spec:
  provider:
    tags:
      InternalAccountingTag: 1234
      dev.corp.net/app: Calculator
      dev.corp.net/team: MyTeam
```

### Metadata Options

Control the exposure of [Instance Metadata Service](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/ec2-instance-metadata.html) on EC2 Instances launched by this provisioner using a generated launch template.

Refer to [recommended, security best practices](https://aws.github.io/aws-eks-best-practices/security/docs/iam/#restrict-access-to-the-instance-profile-assigned-to-the-worker-node) for limiting exposure of Instance Metadata and User Data to pods.

If metadataOptions are omitted from this provisioner, the following default settings will be used.

```
spec:
  provider:
    metadataOptions:
      httpEndpoint: enabled
      httpProtocolIPv6: disabled
      httpPutResponseHopLimit: 2
      httpTokens: required
```

### Amazon Machine Image (AMI) Family

The AMI used when provisioning nodes can be controlled by the `amiFamily` field. Based on the value set for `amiFamily`, Karpenter will automatically query for the appropriate [EKS optimized AMI](https://docs.aws.amazon.com/eks/latest/userguide/eks-optimized-amis.html) via AWS Systems Manager (SSM). When an `amiFamily` of `Custom` is chosen, then an `amiSelector` must be specified that informs Karpenter on which custom AMIs are to be used.

Currently, Karpenter supports `amiFamily` values `AL2`, `Bottlerocket`, `Ubuntu` and `Custom`. GPUs are only supported with `AL2` and `Bottlerocket`.

Note: If a custom launch template is specified, then the AMI value in the launch template is used rather than the `amiFamily` value.


```
spec:
  provider:
    amiFamily: Bottlerocket
```

### Block Device Mappings

The `blockDeviceMappings` field in a Provisioner can be used to control the Elastic Block Storage (EBS) volumes that Karpenter attaches to provisioned nodes. Karpenter uses default block device mappings for the AMI Family specified. For example, the `Bottlerocket` AMI Family defaults with two block device mappings, one for Bottlerocket's control volume and the other for container resources such as images and logs.

Learn more about [block device mappings](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/block-device-mapping-concepts.html).

Note: If a custom launch template is specified, then the `BlockDeviceMappings` field in the launch template is used rather than the provisioner's `blockDeviceMappings`.

```
spec:
  provider:
    blockDeviceMappings:
      - deviceName: /dev/xvda
        ebs:
          volumeSize: 100Gi
          volumeType: gp3
          iops: 10000
          encrypted: true
          kmsKeyID: "arn:aws:kms:us-west-2:111122223333:key/1234abcd-12ab-34cd-56ef-1234567890ab"
          deleteOnTermination: true
          throughput: 125
          snapshotID: snap-0123456789
```

### UserData

You can control the UserData that needs to be applied to your worker nodes via this field. Review the [Custom UserData documentation](../user-data/) to learn the necessary steps
If you need to specify a launch template in addition to UserData, then review the [Launch Template documentation](../launch-templates/) instead and utilize the `spec.providerRef.launchTemplate` field.

### AMISelector

AMISelector is used to configure custom AMIs for Karpenter to use, where the AMIs are discovered through [AWS tags](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/Using_Tags.html), similar to `subnetSelector`. This field is optional, and Karpenter will use the latest EKS-optimized AMIs if an amiSelector is not specified.

EC2 AMIs may be specified by any AWS tag, including `Name`. Selecting tag values using wildcards (`*`) is supported.

EC2 AMI IDs may be specified by using the key `aws-ids` and then passing the IDs as a comma-separated string value.

* When launching nodes, Karpenter automatically determines which architecture a custom AMI is compatible with and will use images that match an instanceType's requirements.
* If multiple AMIs are found that can be used, Karpenter will randomly choose any one.
* If no AMIs are found that can be used, then no nodes will be provisioned.

For additional data on how UserData is configured for Custom AMIs, and how more requirements can be specified for custom AMIs, follow [this documentation](../user-data/#custom-amis).

**Examples**

Select all AMIs with a specified tag:
```
  amiSelector:
    karpenter.sh/discovery/MyClusterName: '*'
```

Select AMIs by name:
```
  amiSelector:
    Name: my-ami
```

Select AMIs by an arbitrary AWS tag key/value pair:
```
  amiSelector:
    MySubnetTag: value
```

Specify AMIs explicitly by ID:
```yaml
  amiSelector:
    aws-ids: "ami-123,ami-456"
```

## spec.provider (Deprecated)

Prior to the introduction of `spec.providerRef`, parameters for the AWS Cloud Provider could be specified within the Provisioner itself through the `spec.provider` field. This field in the Provisioners has now been deprecated, and all fields previously specified through the ProvisionerSpec can now be specified in the `AWSNodeTemplate` CRD instead. See the [upgrade guide for more information](../../upgrade-guide). New parameters can only be specified in the `AWSNodeTemplate` CRD.

## Other Resources

### Accelerators, GPU

Accelerator (e.g., GPU) values include
- `nvidia.com/gpu`
- `amd.com/gpu`
- `aws.amazon.com/neuron`

Karpenter supports accelerators, such as GPUs.


Additionally, include a resource requirement in the workload manifest. This will cause the GPU dependent pod will be scheduled onto the appropriate node.

Here is an example of an accelerator resource in a workload manifest (e.g., pod):

```yaml
spec:
  template:
    spec:
      containers:
      - resources:
          limits:
            nvidia.com/gpu: "1"
```
{{% alert title="Note" color="primary" %}}
If you are provisioning GPU nodes, you need to deploy an appropriate GPU device plugin daemonset for those nodes.
Without the daemonset running, Karpenter will not see those nodes as initialized.
Refer to general [Kubernetes GPU](https://kubernetes.io/docs/tasks/manage-gpus/scheduling-gpus/#deploying-amd-gpu-device-plugin) docs and the following specific GPU docs:
* `nvidia.com/gpu`: [NVIDIA device plugin for Kubernetes](https://github.com/NVIDIA/k8s-device-plugin)
* `amd.com/gpu`: [AMD GPU device plugin for Kubernetes](https://github.com/RadeonOpenCompute/k8s-device-plugin)
* `aws.amazon.com/neuron`: [Kubernetes enviroment setup for Neuron](https://github.com/aws/aws-neuron-sdk/blob/master/neuron-deploy/tutorials/tutorial-k8s.rst)
{{% /alert %}}

