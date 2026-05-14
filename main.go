package main

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/secretsmanager"
	"github.com/golang-jwt/jwt/v5"
)

// IruDevice represents device information from Iru API
type IruDevice struct {
	DeviceID        string `json:"device_id"`
	DeviceName      string `json:"device_name"`
	SerialNumber    string `json:"serial_number"`
	AssetTag        string `json:"asset_tag"`
	User            *IruUser `json:"user"`
	Platform        string `json:"platform"`
	Model           string `json:"model"`
	ModelIdentifier string `json:"model_identifier"`
	LastEnrollment  string `json:"last_enrollment"`
}

// IruDeviceDetails represents the response from the device details endpoint
type IruDeviceDetails struct {
	AutomatedDeviceEnrollment *AutomatedDeviceEnrollment `json:"automated_device_enrollment"`
}

// AutomatedDeviceEnrollment represents ADE info for a device
type AutomatedDeviceEnrollment struct {
	AutoEnrolled bool `json:"auto_enrolled"`
}

// IruUser represents user assignment information
type IruUser struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Email    string `json:"email"`
}

// IruResponse represents the response from Iru list devices API
type IruResponse struct {
	Count   int         `json:"count"`
	Results []IruDevice `json:"results"`
}


// OktaWorkflowResponse represents the response from Okta Workflow
type OktaWorkflowResponse struct {
	TAC      string `json:"tac"`
	Eligible bool   `json:"eligible"`
	Message  string `json:"message"`
}

// OktaTokenResponse represents the OAuth token response from Okta
type OktaTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Scope       string `json:"scope"`
}

// JWTClaims represents the JWT claims for Okta service integration
type JWTClaims struct {
	Issuer    string `json:"iss"`
	Subject   string `json:"sub"`
	Audience  string `json:"aud"`
	ExpiresAt int64  `json:"exp"`
	IssuedAt  int64  `json:"iat"`
	jwt.RegisteredClaims
}

// WorkflowResult represents the result from calling the Okta Workflow
type WorkflowResult struct {
	TAC        string
	StatusCode int
	Message    string
	Success    bool
}

func main() {
	lambda.Start(handleRequest)
}

func handleRequest(ctx context.Context, request events.ALBTargetGroupRequest) (events.ALBTargetGroupResponse, error) {
	// Extract the device serial from the mTLS client certificate subject
	// ALB passes this in x-amzn-mtls-clientcert-subject header
	clientCertSubject := request.Headers["x-amzn-mtls-clientcert-subject"]
	if clientCertSubject == "" {
		log.Printf("All headers: %+v", request.Headers)
		return createErrorResponse(400, "Missing client certificate subject"), nil
	}

	log.Printf("Client certificate subject: %s", clientCertSubject)

	// Parse the device serial from the certificate subject
	// Format: CN=OktaManagementAttestation for DMV6JNFCCP
	serialNumber, err := extractSerialFromClientCert(clientCertSubject)
	if err != nil {
		log.Printf("Error extracting serial from client cert subject: %v", err)
		return createErrorResponse(400, "Invalid client certificate subject"), nil
	}

	if serialNumber == "" {
		return createErrorResponse(400, "No serial number found in certificate"), nil
	}

	log.Printf("Processing request for serial number: %s", serialNumber)

	// 1. Call Iru API to get device and user information
	device, err := getDeviceFromIru(serialNumber)
	if err != nil {
		log.Printf("Error getting device from Iru: %v", err)
		return createErrorResponse(500, "Error looking up device"), nil
	}

	if device == nil {
		return createErrorResponse(404, "Device not found"), nil
	}

	if device.User == nil {
		return createErrorResponse(400, "Device not assigned to user"), nil
	}

	log.Printf("Found device assigned to: %s (%s)", device.User.Name, device.User.Email)

	// Verify the device was auto-enrolled via ADE (calls device details endpoint)
	details, err := getDeviceDetailsFromIru(device.DeviceID)
	if err != nil {
		log.Printf("Error getting device details from Iru: %v", err)
		return createErrorResponse(500, "Error looking up device details"), nil
	}

	if details.AutomatedDeviceEnrollment == nil || !details.AutomatedDeviceEnrollment.AutoEnrolled {
		log.Printf("Device %s is not auto-enrolled via ADE", device.DeviceID)
		return createErrorResponse(403, "Device not auto-enrolled via ADE"), nil
	}

	// Check that the device was enrolled within the last 2 hours
	if err := checkRecentEnrollment(device.LastEnrollment); err != nil {
		log.Printf("Device enrollment check failed: %v", err)
		return createErrorResponse(403, err.Error()), nil
	}

	// 2. Call Okta Workflow to check eligibility and get TAC
	result, err := getTACFromOktaWorkflow(device.User.Email, serialNumber, device.DeviceID)
	if err != nil {
		log.Printf("Error calling Okta Workflow: %v", err)
		return createErrorResponse(500, "Error calling workflow"), nil
	}

	if !result.Success {
		// Return the same status code and message from the workflow
		log.Printf("Workflow returned error %d: %s", result.StatusCode, result.Message)
		return createErrorResponse(result.StatusCode, result.Message), nil
	}

	if result.TAC == "" {
		return createErrorResponse(403, "User not eligible for TAC"), nil
	}

	// Return just the TAC as plain text (no JSON wrapping)
	return events.ALBTargetGroupResponse{
		StatusCode: 200,
		Headers: map[string]string{
			"Content-Type": "text/plain",
		},
		Body: result.TAC,
	}, nil
}

func checkRecentEnrollment(lastEnrollment string) error {
	if lastEnrollment == "" {
		return fmt.Errorf("device has no enrollment date")
	}

	// Parse Iru date format: "2021-03-29 21:46:13.552931+00:00"
	enrolledAt, err := time.Parse("2006-01-02 15:04:05.999999-07:00", lastEnrollment)
	if err != nil {
		return fmt.Errorf("failed to parse enrollment date %q: %w", lastEnrollment, err)
	}

	age := time.Since(enrolledAt)
	log.Printf("Device last enrolled at %s (%.1f minutes ago)", enrolledAt, age.Minutes())

	if age > 2*time.Hour {
		return fmt.Errorf("device enrollment is too old (enrolled %s)", enrolledAt.Format(time.RFC3339))
	}

	return nil
}

func extractSerialFromClientCert(certSubject string) (string, error) {
	// Parse certificate subject: CN=OktaManagementAttestation for DMV6JNFCCP
	// Extract the device ID after "for "

	// Look for CN= in the certificate subject
	if !strings.HasPrefix(certSubject, "CN=") {
		return "", fmt.Errorf("invalid certificate subject format: %s", certSubject)
	}

	// Remove CN= prefix
	cn := strings.TrimPrefix(certSubject, "CN=")

	// Look for "OktaManagementAttestation for " pattern
	pattern := "OktaManagementAttestation for "
	if !strings.HasPrefix(cn, pattern) {
		return "", fmt.Errorf("invalid Okta certificate format: %s", cn)
	}

	// Extract device ID after "for "
	deviceSerial := strings.TrimPrefix(cn, pattern)
	if deviceSerial == "" {
		return "", fmt.Errorf("no device serial found in certificate: %s", cn)
	}

	log.Printf("Extracted device serial: %s", deviceSerial)
	return deviceSerial, nil
}

func getDeviceFromIru(serialNumber string) (*IruDevice, error) {
	iruURL := os.Getenv("IRU_API_URL")
	iruTokenSecretName := os.Getenv("IRU_API_TOKEN_SECRET_NAME")

	if iruURL == "" || iruTokenSecretName == "" {
		return nil, fmt.Errorf("missing Iru API configuration")
	}

	// Get Iru API token from AWS Secrets Manager
	iruToken, err := getSecretValue(iruTokenSecretName)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve Iru API token from Secrets Manager: %w", err)
	}

	// Build the request URL with serial number filter
	// Based on Iru API docs: GET /api/v1/devices?serial_number={serial}
	requestURL := fmt.Sprintf("%s/api/v1/devices?serial_number=%s", iruURL, url.QueryEscape(serialNumber))

	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set authorization header
	req.Header.Set("Authorization", "Bearer "+iruToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call Iru API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Iru API returned status %d", resp.StatusCode)
	}

	// Try to decode as array first (actual API format), then as object
	var devices []IruDevice
	if err := json.NewDecoder(resp.Body).Decode(&devices); err != nil {
		return nil, fmt.Errorf("failed to decode Iru response as array: %w", err)
	}

	if len(devices) == 0 {
		return nil, nil // Device not found
	}

	if len(devices) > 1 {
		log.Printf("Warning: Multiple devices found for serial %s, using first result", serialNumber)
	}

	log.Printf("Found device: %s assigned to user: %s", devices[0].DeviceName, devices[0].User.Email)

	// Return the first (and expected only) matching device
	return &devices[0], nil
}

func getDeviceDetailsFromIru(deviceID string) (*IruDeviceDetails, error) {
	iruURL := os.Getenv("IRU_API_URL")
	iruTokenSecretName := os.Getenv("IRU_API_TOKEN_SECRET_NAME")

	if iruURL == "" || iruTokenSecretName == "" {
		return nil, fmt.Errorf("missing Iru API configuration")
	}

	iruToken, err := getSecretValue(iruTokenSecretName)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve Iru API token from Secrets Manager: %w", err)
	}

	requestURL := fmt.Sprintf("%s/api/v1/devices/%s/details", iruURL, url.PathEscape(deviceID))

	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+iruToken)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call Iru details API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Iru details API returned status %d", resp.StatusCode)
	}

	var details IruDeviceDetails
	if err := json.NewDecoder(resp.Body).Decode(&details); err != nil {
		return nil, fmt.Errorf("failed to decode Iru details response: %w", err)
	}

	return &details, nil
}

func getTACFromOktaWorkflow(userEmail, serialNumber, deviceID string) (*WorkflowResult, error) {
	// Get OAuth access token first
	accessToken, err := getOktaAccessToken()
	if err != nil {
		return nil, fmt.Errorf("failed to get OAuth access token: %w", err)
	}

	oktaWorkflowURL := os.Getenv("OKTA_WORKFLOW_URL")
	if oktaWorkflowURL == "" {
		return nil, fmt.Errorf("missing Okta Workflow URL")
	}

	// Add email as query parameter
	workflowURL, err := url.Parse(oktaWorkflowURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse workflow URL: %w", err)
	}

	query := workflowURL.Query()
	query.Set("email", userEmail)
	workflowURL.RawQuery = query.Encode()

	log.Printf("Calling workflow URL: %s", workflowURL.String())

	// Create GET request with email query parameter (no JSON payload needed)
	req, err := http.NewRequest("GET", workflowURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call Okta Workflow: %w", err)
	}
	defer resp.Body.Close()

	// Read the response body first
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Okta response body: %w", err)
	}

	log.Printf("Okta Workflow response %d: %s", resp.StatusCode, string(body))

	if resp.StatusCode != http.StatusOK {
		// Return the workflow's status code and message
		return &WorkflowResult{
			TAC:        "",
			StatusCode: resp.StatusCode,
			Message:    strings.TrimSpace(string(body)),
			Success:    false,
		}, nil
	}

	// Try to decode as JSON first
	var oktaResp OktaWorkflowResponse
	if err := json.Unmarshal(body, &oktaResp); err != nil {
		// If JSON decode fails, treat response as plain text TAC
		tac := strings.TrimSpace(string(body))
		if tac == "" {
			return &WorkflowResult{
				TAC:        "",
				StatusCode: resp.StatusCode,
				Message:    "Empty response from workflow",
				Success:    false,
			}, nil
		}
		log.Printf("Workflow returned plain text TAC: %s", tac)
		return &WorkflowResult{
			TAC:        tac,
			StatusCode: resp.StatusCode,
			Message:    "",
			Success:    true,
		}, nil
	}

	// JSON response - check eligibility
	if !oktaResp.Eligible {
		log.Printf("User %s not eligible: %s", userEmail, oktaResp.Message)
		return &WorkflowResult{
			TAC:        "",
			StatusCode: resp.StatusCode,
			Message:    oktaResp.Message,
			Success:    false,
		}, nil
	}

	return &WorkflowResult{
		TAC:        oktaResp.TAC,
		StatusCode: resp.StatusCode,
		Message:    oktaResp.Message,
		Success:    true,
	}, nil
}

func getOktaAccessToken() (string, error) {
	// Get configuration from environment variables
	oktaDomain := os.Getenv("OKTA_DOMAIN")
	clientID := os.Getenv("OKTA_CLIENT_ID")
	secretName := os.Getenv("OKTA_PRIVATE_KEY_SECRET_NAME")

	if oktaDomain == "" || clientID == "" || secretName == "" {
		return "", fmt.Errorf("missing Okta OAuth configuration")
	}

	// Get private key from AWS Secrets Manager
	privateKeyPEM, err := getSecretValue(secretName)
	if err != nil {
		return "", fmt.Errorf("failed to retrieve private key from Secrets Manager: %w", err)
	}

	// Parse the private key
	privateKey, err := parsePrivateKey(privateKeyPEM)
	if err != nil {
		return "", fmt.Errorf("failed to parse private key: %w", err)
	}

	// Create JWT assertion
	jwtAssertion, err := createJWTAssertion(oktaDomain, clientID, privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to create JWT assertion: %w", err)
	}

	// Exchange JWT for access token
	tokenURL := fmt.Sprintf("https://%s/oauth2/v1/token", oktaDomain)

	formData := url.Values{
		"grant_type":            {"client_credentials"},
		"client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"},
		"client_assertion":      {jwtAssertion},
		"scope":                 {"okta.workflows.invoke.manage"},
	}

	req, err := http.NewRequest("POST", tokenURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return "", fmt.Errorf("failed to create token request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to call token endpoint: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Read the error response body for debugging
		body, _ := io.ReadAll(resp.Body)
		log.Printf("Token endpoint error %d: %s", resp.StatusCode, string(body))
		log.Printf("Request URL: %s", tokenURL)
		log.Printf("JWT assertion created: %s", jwtAssertion[:50]+"...")
		return "", fmt.Errorf("token endpoint returned status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp OktaTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("failed to decode token response: %w", err)
	}

	return tokenResp.AccessToken, nil
}

func parsePrivateKey(privateKeyPEM string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(privateKeyPEM))
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}

	privateKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		// Try PKCS#8 format
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse private key: %w", err)
		}

		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("private key is not RSA")
		}
		return rsaKey, nil
	}

	return privateKey, nil
}

func createJWTAssertion(oktaDomain, clientID string, privateKey *rsa.PrivateKey) (string, error) {
	now := time.Now()

	claims := JWTClaims{
		Issuer:    clientID,
		Subject:   clientID,
		Audience:  fmt.Sprintf("https://%s/oauth2/v1/token", oktaDomain),
		ExpiresAt: now.Add(5 * time.Minute).Unix(),
		IssuedAt:  now.Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)

	// Generate kid from the public key (same logic as conversion script)
	kid := generateKid(privateKey.PublicKey)
	token.Header["kid"] = kid

	signedToken, err := token.SignedString(privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign JWT: %w", err)
	}

	return signedToken, nil
}

func generateKid(pubKey rsa.PublicKey) string {
	// Generate kid using JWK thumbprint method (RFC 7638) - same as conversion script
	n := base64.RawURLEncoding.EncodeToString(pubKey.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pubKey.E)).Bytes())

	// Create minimal JWK for thumbprint calculation
	thumbprintJWK := map[string]interface{}{
		"kty": "RSA",
		"n":   n,
		"e":   e,
	}

	// Convert to canonical JSON
	canonicalJSON, err := json.Marshal(thumbprintJWK)
	if err != nil {
		log.Printf("Error creating canonical JSON for kid: %v", err)
		return ""
	}

	// Generate SHA256 hash and encode as base64url
	hash := sha256.Sum256(canonicalJSON)
	return base64.RawURLEncoding.EncodeToString(hash[:])
}

func getSecretValue(secretName string) (string, error) {
	// Create AWS session (region will be auto-detected from Lambda environment)
	sess, err := session.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to create AWS session: %w", err)
	}

	// Create Secrets Manager client
	svc := secretsmanager.New(sess)

	// Retrieve the secret
	input := &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(secretName),
	}

	result, err := svc.GetSecretValue(input)
	if err != nil {
		return "", fmt.Errorf("failed to retrieve secret: %w", err)
	}

	if result.SecretString == nil {
		return "", fmt.Errorf("secret value is nil")
	}

	return *result.SecretString, nil
}

func createErrorResponse(statusCode int, message string) events.ALBTargetGroupResponse {
	return events.ALBTargetGroupResponse{
		StatusCode: statusCode,
		Headers: map[string]string{
			"Content-Type": "text/plain",
		},
		Body: message,
	}
}