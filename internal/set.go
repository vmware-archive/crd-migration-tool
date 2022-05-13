// Copyright 2019 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: BSD-2-Clause

package internal

import "strings"

type stringSet map[string]struct{}

func New(values, separator string) stringSet {
	if 0 == len(values) {
		return nil
	}
	array := strings.Split(values, separator)
	set := make(stringSet)
	for _, value := range array {
		set.add(value)
	}
	return set
}

func (s stringSet) add(value string) {
	s[value] = struct{}{}
}

func (s stringSet) has(value string) bool {
	_, found := s[value]
	return found
}

func (s stringSet) remove(value string) {
	delete(s, value)
}
