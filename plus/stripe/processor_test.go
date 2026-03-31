package stripe

import (
	"fmt"
	"testing"
)

func TestPaymentPageExpectedAmount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		init paymentPageInitResponse
		want int
	}{
		{
			name: "prefer total summary due",
			init: paymentPageInitResponse{
				LineItems: []struct {
					Amount int `json:"amount"`
				}{
					{Amount: 100},
					{Amount: 200},
				},
				TotalSummary: struct {
					Due int `json:"due"`
				}{
					Due: 999,
				},
			},
			want: 999,
		},
		{
			name: "sum line items when due missing",
			init: paymentPageInitResponse{
				LineItems: []struct {
					Amount int `json:"amount"`
				}{
					{Amount: 100},
					{Amount: 200},
					{Amount: 300},
				},
			},
			want: 600,
		},
		{
			name: "zero when nothing present",
			init: paymentPageInitResponse{},
			want: 0,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := paymentPageExpectedAmount(tt.init); got != tt.want {
				t.Fatalf("paymentPageExpectedAmount() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestRejectsBeta(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "match wrapped beta rejection",
			err:  fmt.Errorf("init payment page: %w", ResponseError{StatusCode: 400, Body: "beta flag is not supported"}),
			want: true,
		},
		{
			name: "ignore non beta bad request",
			err:  fmt.Errorf("init payment page: %w", ResponseError{StatusCode: 400, Body: "missing key"}),
			want: false,
		},
		{
			name: "ignore non response error",
			err:  fmt.Errorf("init payment page: %w", fmt.Errorf("timeout")),
			want: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := rejectsBeta(tt.err); got != tt.want {
				t.Fatalf("rejectsBeta() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPaymentStatusHelpers(t *testing.T) {
	t.Parallel()

	t.Run("confirm response", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			payload paymentPageConfirmResponse
			want    bool
		}{
			{
				name: "status complete",
				payload: paymentPageConfirmResponse{
					Status: "complete",
				},
				want: true,
			},
			{
				name: "open with succeeded intent",
				payload: paymentPageConfirmResponse{
					Status: "open",
					PaymentIntent: struct {
						ID     string `json:"id"`
						Status string `json:"status"`
					}{
						Status: "succeeded",
					},
				},
				want: true,
			},
			{
				name: "requires action",
				payload: paymentPageConfirmResponse{
					Status: "open",
					PaymentIntent: struct {
						ID     string `json:"id"`
						Status string `json:"status"`
					}{
						Status: "requires_action",
					},
				},
				want: false,
			},
		}

		for _, tt := range tests {
			tt := tt
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				if got := paymentSucceeded(tt.payload); got != tt.want {
					t.Fatalf("paymentSucceeded() = %v, want %v", got, tt.want)
				}
			})
		}
	})

	t.Run("poll response", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name      string
			payload   paymentPagePollResponse
			wantOK    bool
			wantFinal bool
		}{
			{
				name: "poll success",
				payload: paymentPagePollResponse{
					State:               "succeeded",
					PaymentObjectStatus: "succeeded",
				},
				wantOK:    true,
				wantFinal: false,
			},
			{
				name: "processing counts as success",
				payload: paymentPagePollResponse{
					State:               "processing",
					PaymentObjectStatus: "processing",
				},
				wantOK:    true,
				wantFinal: false,
			},
			{
				name: "failed is final",
				payload: paymentPagePollResponse{
					State:               "failed",
					PaymentObjectStatus: "requires_payment_method",
				},
				wantOK:    false,
				wantFinal: true,
			},
		}

		for _, tt := range tests {
			tt := tt
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				if got := paymentPollSucceeded(tt.payload); got != tt.wantOK {
					t.Fatalf("paymentPollSucceeded() = %v, want %v", got, tt.wantOK)
				}
				if got := paymentPollFinal(tt.payload); got != tt.wantFinal {
					t.Fatalf("paymentPollFinal() = %v, want %v", got, tt.wantFinal)
				}
			})
		}
	})
}
