/*
 * SPDX-FileCopyrightText: © 2017-2025 Istari Digital, Inc.
 * SPDX-License-Identifier: Apache-2.0
 */

package cmd

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkloadMix(t *testing.T) {
	testCases := []struct {
		name         string
		workload     string
		read         int
		update       int
		insert       int
		scan         int
		readModWrite int
		readLatest   int
	}{
		{name: "A", workload: "A", read: 50, update: 50},
		{name: "B", workload: "b", read: 95, update: 5},
		{name: "C", workload: "C", read: 100},
		{name: "D", workload: "D", insert: 5, readLatest: 95},
		{name: "E", workload: "E", insert: 5, scan: 95},
		{name: "F", workload: "F", read: 50, readModWrite: 50},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mix, err := workloadMix(tc.workload)
			require.NoError(t, err)
			require.Equal(t, tc.read, mix.weights[ycsbRead])
			require.Equal(t, tc.update, mix.weights[ycsbUpdate])
			require.Equal(t, tc.insert, mix.weights[ycsbInsert])
			require.Equal(t, tc.scan, mix.weights[ycsbScan])
			require.Equal(t, tc.readModWrite, mix.weights[ycsbReadModifyWrite])
			require.Equal(t, tc.readLatest, mix.weights[ycsbReadLatest])
			require.Equal(t, 100, mix.total)
		})
	}
}

func TestWorkloadMixInvalid(t *testing.T) {
	_, err := workloadMix("Z")
	require.Error(t, err)
}

func TestMakeYCSBKey(t *testing.T) {
	key := makeYCSBKey(42, 24)
	require.Len(t, key, 24)
	require.Equal(t, uint64(42), binary.BigEndian.Uint64(key[16:]))
}

func TestMakeYCSBValue(t *testing.T) {
	value := makeYCSBValue(7, 64)
	require.Len(t, value, 64)
	require.Equal(t, uint64(7), binary.BigEndian.Uint64(value[:8]))
}
