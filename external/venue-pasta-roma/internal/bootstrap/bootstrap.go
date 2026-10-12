// Package bootstrap runs this service's one-time startup sequence
// (docs/PLAN.md 8.2 item 1): wait for the platform, register as a webhook
// receiver, then push this venue's own menu.
package bootstrap

import (
	"context"
	"fmt"
	"net/http"
	"time"
	"venue-pasta-roma/internal/generated/partnerclient"
)

// WaitForPlatformReady polls {baseURL}/readyz until it answers 200, or
// returns an error once timeout elapses. Compose's own depends_on only
// proves the api container has started, not that its migrations and seed
// have finished running inside it — this is what actually closes that gap.
func WaitForPlatformReady(ctx context.Context, baseURL string, pollInterval, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 5 * time.Second}

	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/readyz", nil)
		if err == nil {
			if resp, err := client.Do(req); err == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return nil
				}
			}
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("platform at %s did not become ready within %s", baseURL, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// RegisterWebhook points this venue's webhookUrl at selfWebhookURL and
// returns the HMAC secret the platform generated for it. docs/PLAN.md's own
// contract (docs/schema-decisions.md) is that this secret is shown exactly
// once, in this response — there is nowhere else to ever fetch it again,
// which is why this must run before anything else that needs it.
func RegisterWebhook(ctx context.Context, client *partnerclient.ClientWithResponses, selfWebhookURL string) (secret string, err error) {
	resp, err := client.UpdatePartnerVenueWithResponse(ctx, partnerclient.UpdatePartnerVenueJSONRequestBody{
		WebhookUrl: &selfWebhookURL,
	})
	if err != nil {
		return "", fmt.Errorf("call updatePartnerVenue: %w", err)
	}
	if resp.JSON200 == nil {
		return "", fmt.Errorf("updatePartnerVenue: unexpected response (status %d): %s", resp.HTTPResponse.StatusCode, string(resp.Body))
	}
	if resp.JSON200.WebhookSecret == nil {
		return "", fmt.Errorf("updatePartnerVenue: response did not include a webhook secret")
	}
	return *resp.JSON200.WebhookSecret, nil
}
