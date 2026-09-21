//go:build linux

package main

import "testing"

func TestLocalExecutable(t *testing.T) { testAcceptanceExecutable(t) }

func TestLocalCrossProcessControlAndSettlement(t *testing.T) { testCrossProcessControl(t) }
