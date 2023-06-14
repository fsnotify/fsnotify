//go:build linux && !appengine
// +build linux,!appengine

package internal

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// CapabilitySet holds one of the 4 capability set types
type CapabilitySet int

const (
	// CapEffective is the set of capabilities used by the kernel to perform permission checks for the thread.
	CapEffective CapabilitySet = 0
	// CapPermitted is the limiting superset for the effective capabilities that the thread may assume.
	CapPermitted CapabilitySet = 1
	// CapInheritable is the set of capabilities preserved across an execve(2). CapInheritable capabilities
	// remain inheritable when executing any program, and inheritable capabilities are added to the
	// permitted set when executing a program that has the corresponding bits set in the file
	// inheritable set.
	CapInheritable CapabilitySet = 2
	// CapBounding is a mechanism that can be used to limit the capabilities that are gained during execve(2).
	CapBounding CapabilitySet = 3
	// CapAmbient set of capabilities that are preserved across an execve(2) of a program that is not privileged.
	// The ambient capability set obeys the invariant that no capability can ever be ambient if it is not
	// both permitted and inheritable.
	CapAmbient CapabilitySet = 4
)

// capabilityV1 is the Capability structure for LINUX_CAPABILITY_VERSION_1
type capabilityV1 struct {
	header unix.CapUserHeader
	data   unix.CapUserData
}

// capabilityV3 is the Capability structure for LINUX_CAPABILITY_VERSION_2
// or LINUX_CAPABILITY_VERSION_3
type capabilityV3 struct {
	header  unix.CapUserHeader
	datap   [2]unix.CapUserData
	bounds  [2]uint32
	ambient [2]uint32
}

// Capabilities holds the capabilities header and data
type Capabilities struct {
	v3 capabilityV3
	v1 capabilityV1
	// Version has values 1, 2 or 3 depending on the kernel version.
	// Prior to 2.6.25 value is set to 1.
	// For Linux 2.6.25 added 64-bit capability sets the value is set to 2.
	// For Linux 2.6.26 and later the value is set to 3.
	Version int
}

// CapInit sets a capability state pointer to the initial capability state.
// The call probes the kernel to determine the capabilities version. After
// Init Capability.Version is set.
// The initial value of all flags are cleared. The Capabilities value can be
// used to get or set capabilities.
func CapInit() (*Capabilities, error) {
	var header unix.CapUserHeader
	var capability Capabilities
	err := unix.Capget(&header, nil)
	if err != nil {
		return nil, errors.New("unable to probe capability version")
	}
	switch header.Version {
	case unix.LINUX_CAPABILITY_VERSION_1:
		capability.Version = 1
		capability.v1.header = header
	case unix.LINUX_CAPABILITY_VERSION_2:
		capability.Version = 2
		capability.v3.header = header
	case unix.LINUX_CAPABILITY_VERSION_3:
		capability.Version = 3
		capability.v3.header = header
	default:
		panic("Unsupported Linux capability version")
	}
	return &capability, nil
}

// IsSet returns true if the capability from the capability list
// (unix.CAP_*) is set for the current process in the capSet CapabilitySet.
// Returns false with nil error if the capability is not set.
// Returns false with an error if there was an error getting capability.
func (c *Capabilities) IsSet(capability int, capSet CapabilitySet) (bool, error) {
	if c.Version < 1 || c.Version > 3 {
		return false, errors.New("invalid capability version")
	}
	if c.Version == 1 {
		c.v1.header.Version = unix.LINUX_CAPABILITY_VERSION_1
		c.v1.header.Pid = int32(os.Getpid())
		err := unix.Capget(&c.v1.header, &c.v1.data)
		if err != nil {
			return false, err
		}
		return c.v1.isSet(capability, capSet), nil
	}
	if c.Version == 2 {
		c.v3.header.Version = unix.LINUX_CAPABILITY_VERSION_2
	} else if c.Version == 3 {
		c.v3.header.Version = unix.LINUX_CAPABILITY_VERSION_3
	}
	c.v3.header.Pid = int32(os.Getpid())
	err := unix.Capget(&c.v3.header, &c.v3.datap[0])
	if err != nil {
		return false, err
	}
	return c.v3.isSet(capability, capSet), nil
}

func (v1 *capabilityV1) isSet(capability int, capSet CapabilitySet) bool {
	switch capSet {
	case CapEffective:
		return (1<<uint(capability))&v1.data.Effective != 0
	case CapPermitted:
		return (1<<uint(capability))&v1.data.Permitted != 0
	case CapInheritable:
		return (1<<uint(capability))&v1.data.Inheritable != 0
	}
	return false
}

func (v3 *capabilityV3) isSet(capability int, capSet CapabilitySet) bool {

	var i uint

	bitIndex := capability
	if bitIndex > 31 {
		i = 1
		bitIndex %= 32
	}
	switch capSet {
	case CapEffective:
		return (1<<uint(bitIndex))&v3.datap[i].Effective != 0
	case CapPermitted:
		return (1<<uint(bitIndex))&v3.datap[i].Permitted != 0
	case CapInheritable:
		return (1<<uint(bitIndex))&v3.datap[i].Inheritable != 0
	case CapBounding:
		return (1<<uint(bitIndex))&v3.bounds[i] != 0
	case CapAmbient:
		return (1<<uint(bitIndex))&v3.ambient[i] != 0
	}
	return false
}
