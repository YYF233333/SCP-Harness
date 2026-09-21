//go:build windows && release

package main

import "testing"

func TestAcceptanceExecutable(t *testing.T) { testAcceptanceExecutable(t) }

func TestR1bCrossProcessControlAndSettlement(t *testing.T) { testCrossProcessControl(t) }
