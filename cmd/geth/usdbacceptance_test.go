// Copyright 2026 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package main

import "testing"

func TestParseAcceptanceCheckpoint(t *testing.T) {
	tests := []struct {
		input string
		want  *uint64
		ok    bool
	}{
		{input: "latest", want: nil, ok: true},
		{input: "", want: nil, ok: true},
		{input: "42", want: uint64Pointer(42), ok: true},
		{input: "0x2a", want: uint64Pointer(42), ok: true},
		{input: "-1", ok: false},
		{input: "head", ok: false},
	}
	for _, test := range tests {
		got, err := parseAcceptanceCheckpoint(test.input)
		if test.ok != (err == nil) {
			t.Fatalf("parseAcceptanceCheckpoint(%q) error=%v", test.input, err)
		}
		if test.want == nil {
			if got != nil {
				t.Fatalf("parseAcceptanceCheckpoint(%q)=%d, want latest", test.input, *got)
			}
		} else if got == nil || *got != *test.want {
			t.Fatalf("parseAcceptanceCheckpoint(%q)=%v, want %d", test.input, got, *test.want)
		}
	}
}

func uint64Pointer(value uint64) *uint64 {
	return &value
}
