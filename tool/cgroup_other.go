//go:build !linux

package tool

import (
	"context"
	"os/exec"
)

// No cgroups here: the kernel tier is absent and the sampler is all there is.
type cgroupTier struct{}
type cgroupTask struct{}

func newCgroupTier(memPolicy) *cgroupTier                 { return nil }
func (*cgroupTier) prepare(string, *exec.Cmd) *cgroupTask { return nil }
func (*cgroupTier) close()                                {}
func (*cgroupTask) started()                              {}
func (*cgroupTask) discard()                              {}
func (*cgroupTask) kill()                                 {}
func (*cgroupTask) finish(context.Context) string         { return "" }
