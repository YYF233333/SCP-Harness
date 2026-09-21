//go:build windows && release

package core

import "testing"

func TestR1bTaskControlIdentityAndEntrypoints(t *testing.T) { testTaskControlEntrypoints(t) }
