// Copyright 2019 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: BSD-2-Clause

package internal

type stringSet map[string]struct{}

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
