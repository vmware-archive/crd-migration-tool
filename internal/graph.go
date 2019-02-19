// Copyright 2019 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: BSD-2-Clause

package internal

import "github.com/pkg/errors"

type graph struct {
	nodes map[string]struct{}
	edges map[string][]string
}

func newGraph() *graph {
	return &graph{
		nodes: make(map[string]struct{}),
		edges: make(map[string][]string),
	}
}

func (g *graph) addEdge(from, to string) {
	if _, found := g.nodes[from]; !found {
		g.nodes[from] = struct{}{}
	}
	if _, found := g.nodes[to]; !found {
		g.nodes[to] = struct{}{}
	}
	edges := g.edges[from]
	if edges == nil {
		edges = make([]string, 0)
	}
	edges = append(edges, to)
	g.edges[from] = edges
}

type dfsSort struct {
	g         *graph
	temp      stringSet
	permanent stringSet
	sorted    []string
}

func (g *graph) sort() ([]string, error) {
	d := &dfsSort{
		g:         g,
		temp:      make(stringSet),
		permanent: make(stringSet),
		sorted:    make([]string, 0),
	}

	return d.sort()
}

func (d *dfsSort) sort() ([]string, error) {
	for node := range d.g.nodes {
		if d.temp.has(node) || d.permanent.has(node) {
			continue
		}

		if err := d.recurse(node); err != nil {
			return nil, err
		}
	}

	return d.sorted, nil
}

func (d *dfsSort) recurse(node string) error {
	if d.permanent.has(node) {
		return nil
	}
	if d.temp.has(node) {
		return errors.Errorf("cycle")
	}

	d.temp.add(node)

	for _, neighbor := range d.g.edges[node] {
		if err := d.recurse(neighbor); err != nil {
			return err
		}
	}

	d.permanent.add(node)
	d.sorted = append([]string{node}, d.sorted...)
	return nil
}
