// Copyright 2019 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: BSD-2-Clause

package main

import (
	context2 "context"
	"fmt"
	"k8s.io/apimachinery/pkg/util/wait"
	"os"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"github.com/vmware/crd-migration-tool/internal"
)

func main() {
	options := internal.Options{
		LogLevel: logrus.InfoLevel.String(),
		QPS:      float32(50.0),
		Burst:    100,
	}
	pflag.StringVar(&options.Resources, "resources", options.LogLevel, "limit resource for migration,use plural name,separator is ',' (e.g: pods,jobs)")
	pflag.StringVar(&options.LogLevel, "log-level", options.LogLevel, "log level")
	pflag.StringVar(&options.Kubeconfig, "kubeconfig", options.Kubeconfig, "path to kubeconfig file")
	pflag.StringVar(&options.Context, "context", options.Context, "specific context to use in the kubeconfig file")
	pflag.StringVar(&options.OldGroupVersion, "from", options.OldGroupVersion, "the old groupVersion")
	pflag.StringVar(&options.NewGroupVersion, "to", options.NewGroupVersion, "the new groupVersion")
	pflag.Float32Var(&options.QPS, "qps", options.QPS, "client requests per second")
	pflag.IntVar(&options.Burst, "burst", options.Burst, "client burst")
	pflag.StringSliceVar(&options.NamespaceMappings, "namespace-mappings", options.NamespaceMappings, "specify from:to changes for item namespaces")
	pflag.StringSliceVar(&options.LabelMappings, "label-mappings", options.LabelMappings, "specify from:to changes for label keys (e.g. example.com:example.io changes all label key occurrences of example.com to example.io)")
	pflag.StringSliceVar(&options.AnnotationMappings, "annotation-mappings", options.AnnotationMappings, "specify from:to changes for annotations keys (e.g. example.com:example.io changes all label key occurrences of example.com to example.io)")
	pflag.StringSliceVar(&options.UpdateOwnerRefMappings, "update-owner-refs", options.UpdateOwnerRefMappings, "specify parent:child ownerRef relationships that need to be updated (e.g. parent:child updates all child resources' ownerRefs to point to the new parent resources)")
	pflag.Parse()

	if len(os.Args) == 1 {
		fmt.Fprintf(os.Stdout, "Usage of %s:\n", os.Args[0])
		pflag.PrintDefaults()
		os.Exit(0)
	}
	context := context2.Background()
	go wait.Until(internal.NewMigrator(options).MigrateSomeResources, 2 * time.Second, context.Done())
	<-context.Done()
}
