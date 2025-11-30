package turnstile

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const verifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

type Verifier struct {
	secret string
	client *http.Client
}

type verifyResponse struct {
	Success    bool     `json:"success"`
	ErrorCodes []string `json:"error-codes"`
	Hostname   string   `json:"hostname"`
}

func NewVerifier(secret string) *Verifier {
	return &Verifier{
		secret: secret,
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

func (v *Verifier) Verify(ctx context.Context, token, remoteIP string) (bool, error) {
	values := url.Values{}
	values.Set("secret", v.secret)
	values.Set("response", token)
	if remoteIP != "" {
		values.Set("remoteip", remoteIP)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, verifyURL, strings.NewReader(values.Encode()))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := v.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	var body verifyResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false, err
	}

	return body.Success, nil
}
