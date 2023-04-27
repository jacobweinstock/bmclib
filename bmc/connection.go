package bmc

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"
)

// Opener interface for opening a connection to a BMC
type Opener interface {
	Open(ctx context.Context) error
}

// Closer interface for closing a connection to a BMC
type Closer interface {
	Close(ctx context.Context) error
}

// connectionProviders is an internal struct to correlate an implementation/provider and its name
type connectionProviders struct {
	name   string
	closer Closer
}

type data struct {
	err    error
	opened interface{}
	data   md
}

type md struct {
	providerAttempted string
	successfulOpen    bool
}

func doOpen(ctx context.Context, opener interface{}, opened chan data) {
	var d data
	switch p := opener.(type) {
	case Opener:
		providerName := getProviderName(opener)
		d.data.providerAttempted = providerName
		if err := p.Open(ctx); err != nil {
			d.err = fmt.Errorf("provider: %v: %w", providerName, err)
			break
		}

		d.data.successfulOpen = true
		d.opened = opener
	default:
		d.err = fmt.Errorf("not an Opener implementation: %T", p)
	}

	opened <- d
}

// OpenConnectionFromInterfaces will try all opener interfaces and remove failed ones.
// The reason failed ones need to be removed is so that when other methods are called (like powerstate)
// implementations that have connections wont nil pointer error when their connection fails.
func OpenConnectionFromInterfaces(ctx context.Context, timeout time.Duration, providers []interface{}) ([]interface{}, Metadata, error) {
	dataChan := make(chan data, len(providers))
	for _, provider := range providers {
		tmpctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		go func(ctx context.Context, opener interface{}, opened chan data) {
			doOpen(ctx, opener, opened)
		}(tmpctx, provider, dataChan)
	}

	var err error
	var opened []interface{}
	var metadataLocal Metadata
	for i := 0; i < len(providers); i++ {
		select {
		case <-ctx.Done():
			err = multierror.Append(err, ctx.Err())
			return nil, Metadata{}, err
		case d, ok := <-dataChan:
			if !ok {
				err = multierror.Append(err, errors.New("data channel closed unexpectedly"))
				break
			}
			if d.err != nil {
				err = multierror.Append(err, d.err)
			}
			if d.opened != nil {
				opened = append(opened, d.opened)
			}
			metadataLocal.ProvidersAttempted = append(metadataLocal.ProvidersAttempted, d.data.providerAttempted)
			if d.data.successfulOpen {
				metadataLocal.SuccessfulOpenConns = append(metadataLocal.SuccessfulOpenConns, d.data.providerAttempted)
			}
		}
	}
	if len(opened) == 0 {
		return nil, metadataLocal, multierror.Append(err, errors.New("no Opener implementations found"))
	}

	return opened, metadataLocal, nil
}

// closeConnection closes a connection to a BMC, trying all interface implementations passed in
func closeConnection(ctx context.Context, c []connectionProviders) (metadata Metadata, err error) {
	var metadataLocal Metadata
	var connClosed bool

	for _, elem := range c {
		if elem.closer == nil {
			continue
		}
		metadataLocal.ProvidersAttempted = append(metadataLocal.ProvidersAttempted, elem.name)
		closeErr := elem.closer.Close(ctx)
		if closeErr != nil {
			err = multierror.Append(err, errors.WithMessagef(closeErr, "provider: %v", elem.name))
			continue
		}
		connClosed = true
		metadataLocal.SuccessfulCloseConns = append(metadataLocal.SuccessfulCloseConns, elem.name)
	}
	if connClosed {
		return metadataLocal, nil
	}
	return metadataLocal, multierror.Append(err, errors.New("failed to close connection"))
}

// CloseConnectionFromInterfaces identifies implementations of the Closer() interface and and passes the found implementations to the closeConnection() wrapper
func CloseConnectionFromInterfaces(ctx context.Context, generic []interface{}) (metadata Metadata, err error) {
	closers := make([]connectionProviders, 0)
	for _, elem := range generic {
		temp := connectionProviders{name: getProviderName(elem)}
		switch p := elem.(type) {
		case Closer:
			temp.closer = p
			closers = append(closers, temp)
		default:
			e := fmt.Sprintf("not a Closer implementation: %T", p)
			err = multierror.Append(err, errors.New(e))
		}
	}
	if len(closers) == 0 {
		return metadata, multierror.Append(err, errors.New("no Closer implementations found"))
	}
	return closeConnection(ctx, closers)
}
