//go:build linux

package scheduler

import "testing"

func TestLocalVortonWalkthrough(t *testing.T) { testFrozenVortonA10(t) }

func TestLocalWorkerLifecycle(t *testing.T) { testRealWorkerLifecycle(t) }

func TestLocalSchedulerControl(t *testing.T) { testRunningSchedulerControl(t) }

func TestLocalControlExitRequiresExplicitRecovery(t *testing.T) { testControlExitRecovery(t) }

func TestLocalCoreCrashCapturesWorkerAndChargesFullLease(t *testing.T) { testCoreCrashRecovery(t) }
