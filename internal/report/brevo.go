package report

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	"vps-watchdog/internal/config"
)

const brevoAPIURL = "https://api.brevo.com/v3/smtp/email"

// BrevoClient sends emails via the Brevo transactional email API.
type BrevoClient struct {
	cfg        *config.BrevoConfig
	recipients []string
}

// Attachment represents an email attachment (e.g., a PNG chart).
type Attachment struct {
	Name    string
	Content []byte // raw bytes — will be base64-encoded before sending
}

// New creates a BrevoClient from the global config.
func New(cfg *config.Config) *BrevoClient {
	return &BrevoClient{
		cfg:        &cfg.Brevo,
		recipients: cfg.Recipients.Emails,
	}
}

// Send sends an HTML email with optional attachments.
func (c *BrevoClient) Send(subject, htmlBody string, attachments []Attachment) error {
	if c.cfg.APIKey == "" || c.cfg.APIKey == "YOUR_BREVO_API_KEY" {
		log.Printf("brevo: API key not configured, skipping email: %s", subject)
		return nil
	}

	type recipient struct {
		Email string `json:"email"`
	}
	type sender struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	type attachment struct {
		Name    string `json:"name"`
		Content string `json:"content"` // base64
	}

	to := make([]recipient, 0, len(c.recipients))
	for _, email := range c.recipients {
		to = append(to, recipient{Email: email})
	}

	attachList := make([]attachment, 0, len(attachments))
	for _, a := range attachments {
		attachList = append(attachList, attachment{
			Name:    a.Name,
			Content: base64.StdEncoding.EncodeToString(a.Content),
		})
	}

	payload := map[string]interface{}{
		"sender":      sender{Name: c.cfg.SenderName, Email: c.cfg.SenderEmail},
		"to":          to,
		"subject":     subject,
		"htmlContent": htmlBody,
	}
	if len(attachList) > 0 {
		payload["attachment"] = attachList
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("brevo: marshal payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, brevoAPIURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("brevo: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", c.cfg.APIKey)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("brevo: send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 201 {
		return fmt.Errorf("brevo: unexpected status %d: %s", resp.StatusCode, string(respBody))
	}

	log.Printf("brevo: email sent successfully: %s (recipients: %v)", subject, c.recipients)
	return nil
}

// SendAlert is a convenience method that satisfies the action.EmailSender interface.
func (c *BrevoClient) SendAlert(subject, body string) error {
	return c.Send(subject, body, nil)
}
