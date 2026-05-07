# New Hire TAC Generator - Development Documentation

This document provides technical context and implementation details for future Claude interactions with this codebase.

## Project Overview

**Purpose**: Generate Temporary Access Codes (TACs) for new hires using mTLS device authentication
**Integration**: Called via custom script in iru-content during laptop setup process
**Architecture**: AWS ALB + Lambda + Go + mTLS + OAuth 2.0 + MDM/Workflow APIs
**Target User**: john.heasman@proof.com (primary test user)

## Key Files and Structure

```
.
├── main.go                    # Complete Lambda function implementation
├── Makefile.aws              # AWS CLI deployment automation
├── README.md                 # User-facing documentation
├── CLAUDE.md                 # This file - development context
├── go.mod                    # Go module dependencies
├── okta_jwk.go               # JWK conversion utility
├── okta_ca_cert.pem          # CA certificate for mTLS validation
├── okta_private_key.pem      # Private key for OAuth JWT signing
├── test-event.json           # Lambda test payload
└── lambda-env-update.json    # Environment variables template
```

## Core Implementation Details

### 1. Certificate Subject Parsing (`main.go:153-179`)
```go
func extractSerialFromClientCert(certSubject string) (string, error)
```
- **Input Format**: `"CN=OktaManagementAttestation for DMV6JNFCCP"`
- **Output**: Device serial number (`"DMV6JNFCCP"`)
- **Critical**: Must match this exact format - Okta-managed certificates only

### 2. Iru MDM Integration (`main.go:181-237`)
```go
func getDeviceFromIru(serialNumber string) (*IruDevice, error)
```
- **API**: `GET https://proof.api.kandji.io/api/v1/devices?serial_number={serial}`
- **Response Format**: Array of devices (not wrapped object)
- **Key Fields**: `user.email`, `device_id`, `device_name`
- **Authentication**: Bearer token from AWS Secrets Manager

### 3. OAuth 2.0 Flow (`main.go:295-363`)
```go
func getOktaAccessToken() (string, error)
```
- **Grant Type**: `client_credentials` with JWT assertion
- **Scope**: `okta.workflows.invoke.manage` (critical - must be granted)
- **JWT Claims**: Standard OAuth JWT with `kid` header from public key thumbprint
- **Token Endpoint**: `https://proof.okta.com/oauth2/v1/token`

### 4. Workflow Integration (`main.go:239-327`)
```go
func getTACFromOktaWorkflow(userEmail, serialNumber, deviceID string) (string, error)
```
- **URL Format**: `{workflow_url}?email={user_email}`
- **Method**: POST with JSON payload + email query parameter
- **Response Handling**: Supports both plain text TAC and JSON responses
- **Error Recovery**: Detailed logging of workflow errors

## Business Logic & Workflow Safeguards

The Okta Workflow implements strict eligibility requirements:

### 1. **Admin Check**
- User must **NOT** be a Super Admin in Okta
- Prevents privileged users from bypassing security controls

### 2. **Group Membership**
- User must be in the `Users: Device Bootstrap` group
- **Critical**: This group is cleared nightly
- Provides time-limited access window for device setup

### 3. **New Hire Validation**
- User must be a recent hire: `now() - employmentDate <= 7 days`
- `employmentDate` is an Okta profile attribute
- Start date cannot be in the future (`>= 0 days`)
- Prevents TAC generation for existing employees

### 4. **Implementation Notes**
- These checks happen in the Okta Workflow, not the Lambda function
- Lambda receives either a TAC or "User not eligible" response
- Business logic changes should be made in the workflow, not code

## Critical Configuration

### Environment Variables
```json
{
  "IRU_API_URL": "https://proof.api.kandji.io",
  "IRU_API_TOKEN_SECRET_NAME": "new-hire-tac/iru-api-token",
  "OKTA_DOMAIN": "proof.okta.com",
  "OKTA_CLIENT_ID": "0oa12qgvzx92ZbuKt698",
  "OKTA_WORKFLOW_URL": "https://proof.workflows.okta.com/api/flo/d034bb1893473dfdc2d504f32e805a0f/invoke",
  "OKTA_PRIVATE_KEY_SECRET_NAME": "new-hire-tac/okta-private-key"
}
```

**Key Notes**:
- `OKTA_WORKFLOW_URL` comes from the Okta Workflow "API Endpoint" card
- `OKTA_PRIVATE_KEY_SECRET_NAME` should match the actual secret name in AWS Secrets Manager

### AWS Resources
- **Lambda Function**: `new-hire-tac-generator` (us-east-1)
- **ALB**: `new-hire-tac-alb` with mTLS Trust Store
- **Domain**: `tac.it.proof.com` (CNAME to ALB)
- **IAM Role**: `new-hire-tac-lambda-role`
- **Secrets**: 
  - `new-hire-tac/okta-private-key` (RSA PEM format)
  - `new-hire-tac/iru-api-token` (Bearer token)

## Data Structures

### Request Flow Data
```go
// ALB Header Input
x-amzn-mtls-clientcert-subject: "CN=OktaManagementAttestation for DMV6JNFCCP"

// Iru Device Response
type IruDevice struct {
    DeviceID        string   `json:"device_id"`         // "5c82484b-af59-4747-9be8-68378e6f50f4"
    DeviceName      string   `json:"device_name"`       // "Proof-DMV6JNFCCP"
    SerialNumber    string   `json:"serial_number"`     // "DMV6JNFCCP"
    AssetTag        string   `json:"asset_tag"`
    User            *IruUser `json:"user"`              // User assignment
    Platform        string   `json:"platform"`          // "Mac"
    Model           string   `json:"model"`
    ModelIdentifier string   `json:"model_identifier"`
}

// Workflow Request
{
  "userEmail": "john.heasman@proof.com",
  "serialNumber": "DMV6JNFCCP", 
  "deviceId": "5c82484b-af59-4747-9be8-68378e6f50f4"
}

// Workflow Response (flexible)
"ABC123DEF456"  // Plain text TAC (preferred)
// OR
{
  "tac": "ABC123DEF456",
  "eligible": true,
  "message": "User is eligible"
}
```

## Common Development Patterns

### 1. Error Handling
- All external API calls include detailed error logging
- HTTP status codes map to specific error types:
  - 400: Invalid certificate format
  - 403: User not eligible  
  - 404: Device not found
  - 500: Internal/API errors

### 2. Logging Strategy
```go
log.Printf("Client certificate subject: %s", clientCertSubject)
log.Printf("Found device: %s assigned to user: %s", deviceName, userEmail)
log.Printf("Calling workflow URL: %s", workflowURL.String())
log.Printf("Workflow returned plain text TAC: %s", tac)
```

### 3. Secret Management
```go
func getSecretValue(secretName string) (string, error)
```
- Uses AWS SDK with Lambda's auto-configured session
- Secrets Manager integration for sensitive data
- Region auto-detection from Lambda environment

## Deployment Workflow

### 1. Code Changes
```bash
# Build (creates out/bootstrap binary)
make -f Makefile.aws build

# Deploy updated Lambda function
make -f Makefile.aws update-lambda

# Test immediately  
CURL_SSL_BACKEND=secure-transport /usr/bin/curl --cert "OktaManagementAttestation for DMV6JNFCCP" https://tac.it.proof.com
```

### 2. Build Process
- **Binary Output**: `out/bootstrap` (keeps root directory clean)
- **Deployment Package**: `lambda-function.zip` (root directory for AWS CLI compatibility)
- **Clean**: `make -f Makefile.aws clean` removes both binary and zip

### 2. Environment Updates
```bash
# Update environment variables
aws lambda update-function-configuration \
  --function-name new-hire-tac-generator \
  --environment file://lambda-env-update.json
```

### 3. Debugging
```bash
# Check recent logs
aws logs tail /aws/lambda/new-hire-tac-generator --follow

# Direct invocation
aws lambda invoke --function-name new-hire-tac-generator --payload fileb://test-event.json /tmp/response.json
```

## Historical Context & Lessons Learned

### 1. **Integration Context**
- **Purpose**: System is called by iru-content script during laptop setup
- **Workflow**: Device → Certificate → TAC → Device Bootstrap
- **Scope**: Focused on new hire onboarding automation

### 2. Certificate Header Discovery
- **Initial assumption**: ALB would use `x-forwarded-client-cert`
- **Reality**: ALB uses `x-amzn-mtls-clientcert-subject`
- **Impact**: Required code change to read correct header

### 3. Iru API Response Format
- **Initial assumption**: Response wrapped in `{count, results}` object
- **Reality**: Direct array response `[{device1}, {device2}]`
- **Impact**: Simplified JSON parsing logic

### 4. OAuth Scope Evolution
- **Progression**: `invalid_scope` → `consent_required` → `no_org_scopes_granted` → success
- **Solution**: User granted `okta.workflows.invoke.manage` scope manually
- **Current**: Scope must be explicitly requested in token request

### 5. Workflow Parameter Format
- **Issue**: Initial 405 errors due to placeholder URL
- **Issue**: 500 errors due to missing email query parameter
- **Solution**: Email must be passed as `?email=user@domain.com` query parameter
- **Current**: Both query parameter and JSON payload required

### 6. Response Format Flexibility
- **Evolution**: JSON-only → JSON + plain text support
- **Driver**: Workflow team simplified response format
- **Implementation**: Try JSON decode first, fallback to plain text

### 7. Business Logic Implementation
- **Discovery**: Workflow handles all eligibility logic (Admin check, group membership, hire date)
- **Architecture**: Lambda focuses on technical integration, Workflow handles business rules
- **Benefit**: Business logic changes don't require code deployment

## Security Model

### 1. Authentication Chain
```
Client Cert → ALB Trust Store → Certificate Subject → Device Lookup → User Email → OAuth → Workflow
```

### 2. Certificate Requirements
- **Issuer**: Must be signed by CA in ALB Trust Store
- **Subject Format**: `CN=OktaManagementAttestation for {SERIAL}`
- **Storage**: macOS Secure Enclave (production) or files (development)

### 3. API Security
- **Iru API**: Bearer token authentication, stored in AWS Secrets Manager
- **Okta OAuth**: JWT assertion with RSA signature, short-lived tokens
- **AWS**: IAM role-based permissions, no long-lived credentials

## Performance Characteristics

### Typical Request Timeline
```
0ms     - ALB receives mTLS request
50ms    - Certificate validation, Lambda invocation
150ms   - Lambda cold start (if needed)
200ms   - Certificate parsing, device lookup
1500ms  - Iru API response
1600ms  - OAuth token generation
2500ms  - Workflow API call
3000ms  - Response processing, TAC return
```

### Scaling Considerations
- **Concurrency**: Lambda can handle multiple simultaneous requests
- **Rate Limiting**: Iru and Okta APIs may have rate limits
- **Caching**: OAuth tokens could be cached (not implemented)

## Testing Strategies

### 1. End-to-End Testing
```bash
# Primary test case - working device
CURL_SSL_BACKEND=secure-transport /usr/bin/curl --cert "OktaManagementAttestation for DMV6JNFCCP" https://tac.it.proof.com

# Error cases
curl https://tac.it.proof.com  # Should fail - no cert
```

### 2. Component Testing
```bash
# Direct Lambda invocation
aws lambda invoke --function-name new-hire-tac-generator --payload fileb://test-event.json /tmp/response.json

# Certificate parsing test
echo "CN=OktaManagementAttestation for DMV6JNFCCP" | grep -o 'for [A-Z0-9]*'
```

### 3. Monitoring
```bash
# Real-time log monitoring
aws logs tail /aws/lambda/new-hire-tac-generator --follow

# Error filtering
aws logs filter-log-events --log-group-name "/aws/lambda/new-hire-tac-generator" --filter-pattern "Error"
```

## Future Enhancement Opportunities

### 1. Performance Optimizations
- OAuth token caching (respect token TTL)
- Connection pooling for HTTP clients
- Async logging to reduce latency

### 2. Security Enhancements
- Certificate serial validation against device enrollment
- IP allowlisting for additional security
- Request rate limiting per device

### 3. Operational Improvements
- Structured logging (JSON format)
- Custom CloudWatch metrics
- Health check endpoint

### 4. Error Recovery
- Retry logic for transient API failures
- Circuit breaker pattern for dependent services
- Graceful degradation options

## Dependencies & Versions

```go
module new-hire-tac-generator

go 1.21

require (
    github.com/aws/aws-lambda-go v1.46.0
    github.com/aws/aws-sdk-go v1.49.0  
    github.com/golang-jwt/jwt/v5 v5.2.0
)
```

### External Services
- **AWS Lambda**: Runtime provided.al2 
- **Iru MDM**: kandji.io API v1
- **Okta**: OAuth 2.0 + Workflows API
- **AWS**: ALB, ACM, Secrets Manager, CloudWatch

## Contact & Ownership

- **Primary User**: john.heasman@proof.com
- **Test Device**: Proof-DMV6JNFCCP (serial: DMV6JNFCCP)
- **Infrastructure**: AWS account 913807247959, us-east-1
- **Domain**: proof.com IT infrastructure team