#VMware has ended active development of this project, this repository will no longer be updated

# Kubernetes CustomResourceDefinition Migration Tool

[![Build Status](https://travis-ci.org/vmware/crd-migration-tool.svg?branch=master)](https://travis-ci.org/vmware/crd-migration-tool)

## Overview

This tool migrates Kubernetes CustomResourceDefinition (CRD) instances from an "old" API group to a
"new" one.

## Getting Started

1. Go to the [latest release](https://github.com/vmware/crd-migration-tool/releases) and download the tarball for your platform.

1. Extract the tarball locally. For example: 
    
    ```shell
    tar -xzf crd-migration-tool-v1.0.0-linux-amd64.tar.gz
    ```

1. Move the `crd-migrator` binary to somewhere within your $PATH. For example:
    
    ```shell
    mv crd-migrator /usr/local/bin
    ```

## Usage

#### Requirements

- The same CRDs exist in both API groups
- The CRDs in the new API group do not have `spec.subresources.status` (more details on this below)
- Your user has RBAC permissions to get `customresourcedefinitions.apiextensions.k8s.io`
- Your user has RBAC permissions to get instances of all the CRDs in the old API group
- Your user has RBAC permissions to get and create instances of all the CRDs in the new API group
- If you are using namespace remapping, the target namespace(s) already exist

#### Example Scenario

You have an API group called `my.example.com` with CRDs `foos` and `bars`. You want to "rename"
`my.example.com` to `someapp.io`.

Because you can't rename objects in Kubernetes, including `customresourcedefinitions`, we instead
essentially have to "copy and paste" each CRD instance from the old API group to the new one. We do
this by retrieving each instance of a CRD, changing its `apiVersion` from the old API group to the
new one, then creating the new instance under the new API group.

Given the following custom resource `Foo` and `Bar` instances:

```yaml
apiVersion: my.example.com/v1
kind: Foo
metadata:
  namespace: my-example
  name: foo1
  labels:
    my.example.com/color: blue
    some.other.label: abcd
    sub.my.example.com/shape: circle
  annotations:
    my.example.com/color: blue
    some.other.label: abcd
    sub.my.example.com/shape: circle
spec:
  someSpecHere: {}
status:
  someStatusHere: {}
---
apiVersion: my.example.com/v1
kind: Foo
metadata:
  namespace: another-namespace
  name: foo2
  labels:
    my.example.com/color: blue
    some.other.label: abcd
    sub.my.example.com/shape: circle
  annotations:
    my.example.com/color: blue
    some.other.label: abcd
    sub.my.example.com/shape: circle
spec:
  someSpecHere: {}
status:
  someStatusHere: {}
---
apiVersion: my.example.com/v1
kind: Bar
metadata:
  namespace: my-example
  name: bar1
  labels:
    my.example.com/color: blue
    some.other.label: abcd
    sub.my.example.com/shape: circle
  annotations:
    my.example.com/color: blue
    some.other.label: abcd
    sub.my.example.com/shape: circle
spec:
  someSpecHere: {}
status:
  someStatusHere: {}
```

When you run the tool:

```bash
crd-migrator --from my.example.com/v1                         \
             --to someapp.io/v1                               \
             --namespace-mappings my-example:someapp          \
             --annotation-mappings my-example.com:someapp.io  \
             --label-mappings my-example.com:someapp.io
INFO[0000] Starting resource migration                   resource=foos
INFO[0000] Checking if item already exists in new API group  id=someapp/foo1 original-namespace=my-example resource=foos
INFO[0000] Creating item                                 id=someapp/foo1 original-namespace=my-example resource=foos
INFO[0000] Checking if item already exists in new API group  id=another-namespace/foo2 resource=foos
INFO[0000] Creating item                                 id=another-namespace/foo2 resource=foos
INFO[0000] Completed resource migration                  resource=foos
INFO[0000] Starting resource migration                   resource=bars
INFO[0000] Checking if item already exists in new API group  id=someapp/bar1 original-namespace=my-example resource=bars
INFO[0000] Creating item                                 id=someapp/bar1 original-namespace=my-example resource=bars
INFO[0000] Completed resource migration                  resource=bars
```

The result looks like this:

```yaml
apiVersion: someapp.io/v1
kind: Foo
metadata:
  namespace: someapp
  name: foo1
  labels:
    someapp.io/color: blue
    some.other.label: abcd
    sub.someapp.io/shape: circle
  annotations:
    someapp.io/color: blue
    some.other.label: abcd
    sub.someapp.io/shape: circle
spec:
  someSpecHere: {}
status:
  someStatusHere: {}
---
apiVersion: someapp.io/v1
kind: Foo
metadata:
  namespace: another-namespace
  name: foo2
  labels:
    someapp.io/color: blue
    some.other.label: abcd
    sub.someapp.io/shape: circle
  annotations:
    someapp.io/color: blue
    some.other.label: abcd
    sub.someapp.io/shape: circle
spec:
  someSpecHere: {}
status:
  someStatusHere: {}
---
apiVersion: someapp.io/v1
kind: Bar
metadata:
  namespace: someapp
  name: bar1
  labels:
    someapp.io/color: blue
    some.other.label: abcd
    sub.someapp.io/shape: circle
  annotations:
    someapp.io/color: blue
    some.other.label: abcd
    sub.someapp.io/shape: circle
spec:
  someSpecHere: {}
status:
  someStatusHere: {}
```

The following changes are worth noting:

- The `apiVersion` was changed from `my.example.com/v1` to `someapp.io/v1`
- All `Foo` and `Bar` instances in the `my-example` namespace were created in the `someapp` namespace
- All label and annotation keys that referenced `my.example.com` were updated to `someapp.io`

#### CRDs & the status subresource

Non-CRD API types in Kubernetes typically have a distinction between `status` and non-`status`
sections. When you create one of these (such as a `Pod` or `Secret`), Kubernetes usually discards
any `status` details and instead sets these based on whatever logic is appropriate for that type
(it's important to note that this behavior varies by type). Similarly, when updating an item,
updates to `status` are discarded and the current `status` is preserved. If you want to modify the
`status`, you need to use a special `/status` subresource (and when you use this, any changes
outside of `status` are ignored). Typically system controllers are allowed to update `status` data,
but normal users are not.

Kubernetes optionally supports these same `status` and non-`status` distinctions and separations for
CRD instances. This is currently controlled by the `CustomResourceSubresources` feature gate, which
is alpha and disabled by default in Kubernetes v1.10 and is beta and enabled by default in
Kubernetes v1.11 and newer.

When the `CustomResourceSubresources` feature is enabled, this doesn't mean that all CRD instances
adopt the `status`/non-`status` behaviors. Instead, each CRD must [opt in to gain this
functionality](https://kubernetes.io/docs/tasks/access-kubernetes-api/custom-resources/custom-resource-definitions/#subresources).

**HOWEVER**: you *must* remove the `spec.subresources.status` entry from your CRDs before you can
migrate your CRD instances to the new API group. This will allow the tool to migrate each item's
`status` data. Otherwise, it would be lost, and that could lead to undesirable behavior (depending
on the CRD type and any logic associated with it).

You can temporarily remove `spec.subresources.status` from each CRD in the new API group, run the
migration tool, and finally restore `spec.subresources.status` when you have finished migrating.

#### Data in the old API group

This tool does not delete any data in the old API group. If you want to do that, you can use
`kubectl`, such as:

```bash
kubectl delete foos.my.example.com --namespace my-example --all
```

Unfortunately, this must be done per-namespace. If there is a need, we can consider adding support
to this tool to delete items across all namespaces from the old API group.

## Building From Source

#### Prerequisites

- [Go](https://golang.org/doc/install) installed

#### Instructions

1. Clone this repository to your computer

1. Change your working directory to the root directory of the project

1. Build the binary:
    ```shell
    go build ./cmd/crd-migrator
    ```

You now have an executable binary, `crd-migrator`, in your working directory.
