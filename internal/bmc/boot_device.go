package bmc

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"
)

// BootDeviceSetter sets the next boot device for a machine
type BootDeviceSetter interface {
	BootDeviceSet(ctx context.Context, bootDevice string, setPersistent, efiBoot bool) (ok bool, err error)
}

// bootDeviceProviders is an internal struct to correlate an implementation/provider and its name
type bootDeviceProviders struct {
	name             string
	bootDeviceSetter BootDeviceSetter
}

// setBootDevice sets the next boot device.
//
// setPersistent persists the next boot device.
// efiBoot sets up the device to boot off UEFI instead of legacy.
func setBootDevice(ctx context.Context, timeout time.Duration, bootDevice string, setPersistent, efiBoot bool, b []bootDeviceProviders) (ok bool, metadata Metadata, err error) {
	var metadataLocal Metadata

	for _, elem := range b {
		if elem.bootDeviceSetter == nil {
			continue
		}
		select {
		case <-ctx.Done():
			err = multierror.Append(err, ctx.Err())

			return false, metadata, err
		default:
			metadataLocal.ProvidersAttempted = append(metadataLocal.ProvidersAttempted, elem.name)
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			ok, setErr := elem.bootDeviceSetter.BootDeviceSet(ctx, bootDevice, setPersistent, efiBoot)
			if setErr != nil {
				err = multierror.Append(err, errors.WithMessagef(setErr, "provider: %v", elem.name))
				continue
			}
			if !ok {
				err = multierror.Append(err, fmt.Errorf("provider: %v, failed to set boot device", elem.name))
				continue
			}
			metadataLocal.SuccessfulProvider = elem.name
			return ok, metadataLocal, nil
		}
	}
	return ok, metadataLocal, multierror.Append(err, errors.New("failed to set boot device"))
}

// SetBootDeviceFromInterfaces identifies implementations of the BootDeviceSetter interface and passes the found implementations to the setBootDevice() wrapper
func SetBootDeviceFromInterfaces(ctx context.Context, timeout time.Duration, bootDevice string, setPersistent, efiBoot bool, generic []interface{}) (ok bool, metadata Metadata, err error) {
	bdSetters := make([]bootDeviceProviders, 0)
	for _, elem := range generic {
		temp := bootDeviceProviders{name: getProviderName(elem)}
		switch p := elem.(type) {
		case BootDeviceSetter:
			temp.bootDeviceSetter = p
			bdSetters = append(bdSetters, temp)
		default:
			e := fmt.Sprintf("not a BootDeviceSetter implementation: %T", p)
			err = multierror.Append(err, errors.New(e))
		}
	}
	if len(bdSetters) == 0 {
		return ok, metadata, multierror.Append(err, errors.New("no BootDeviceSetter implementations found"))
	}
	return setBootDevice(ctx, timeout, bootDevice, setPersistent, efiBoot, bdSetters)
}
