// Copyright 2019 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: BSD-2-Clause

package internal

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGraph(t *testing.T) {
	g := newGraph()
	g.addEdge("a", "b")
	g.addEdge("b", "c")
	g.addEdge("d", "c")
	g.addEdge("e", "c")
	g.addEdge("f", "b")

	fmt.Println("NODES")
	for _, id := range g.nodes {
		fmt.Println(id)
	}

	fmt.Println("EDGES")
	for from, to := range g.edges {
		fmt.Printf("%s->%s\n", from, to)
	}

	sorted, err := g.sort()
	assert.NoError(t, err)
	fmt.Println("SORT", sorted)
}

func TestGraphWithCycle(t *testing.T) {
	g := newGraph()
	g.addEdge("a", "b")
	g.addEdge("b", "c")
	g.addEdge("d", "c")
	g.addEdge("e", "c")
	g.addEdge("f", "b")
	g.addEdge("c", "f")

	fmt.Println("NODES")
	for _, id := range g.nodes {
		fmt.Println(id)
	}

	fmt.Println("EDGES")
	for from, to := range g.edges {
		fmt.Printf("%s->%s\n", from, to)
	}

	sorted, err := g.sort()
	assert.EqualError(t, err, "cycle")
	assert.Nil(t, sorted)
}
