package plus

import (
	"errors"
	"fmt"
	"strings"
)

func normalizeOptions(opts RegisterOptions) (RegisterOptions, error) {
	opts.DataDir = strings.TrimSpace(opts.DataDir)
	if opts.DataDir == "" {
		return RegisterOptions{}, errors.New("data dir is empty")
	}
	if opts.Purchase.Enabled {
		opts.Purchase.RevenueCatBearerKey = strings.TrimSpace(opts.Purchase.RevenueCatBearerKey)
		if opts.Purchase.RevenueCatBearerKey == "" {
			return RegisterOptions{}, errors.New("purchase revenuecat bearer key is empty")
		}
		if opts.Purchase.Store == nil {
			return RegisterOptions{}, errors.New("purchase token store is nil")
		}
	}
	if opts.OTPWait <= 0 {
		opts.OTPWait = defaultOTPWait
	}
	if opts.OTPPoll <= 0 {
		opts.OTPPoll = defaultOTPPoll
	}
	opts.RegistrationProxyPool = opts.RegistrationProxyPool.cleaned()
	if len(opts.RegistrationProxyPool) == 0 {
		return RegisterOptions{}, errors.New("proxy pool is empty")
	}
	return opts, nil
}

func newRegistrationFlow(cfg RegisterOptions) (*registrationFlow, error) {
	client, err := newClient(cfg)
	if err != nil {
		return nil, err
	}

	return &registrationFlow{
		cfg:    cfg,
		client: client,
	}, nil
}

func (r *registrationFlow) resetAuthSession() error {
	client, err := r.client.Refresh()
	if err != nil {
		return fmt.Errorf("build fresh http client: %w", err)
	}
	r.client = client
	r.oaiDID = ""
	return nil
}
