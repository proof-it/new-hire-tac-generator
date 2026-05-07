# New Hire TAC Generator

A lambda function, written in Go and fronted by an ALB, that generates a Temporary Access Code (TAC) for new hires based on device serial numbers from Okta Management Attestation client certificates. This endpoint is called via a custom script (in iru-content) during laptop setup. It maps devices to user via an API call to Iru, then invokes an Okta Workflow to do the heavy lifting.

## Architecture

```
Client with mTLS cert -> ALB (validates cert) -> Lambda -> Iru API -> Okta Workflow -> TAC
```

1. Client presents mTLS certificate with laptop serial as CN format: `CN=OktaManagementAttestation for {DEVICE_SERIAL}`
2. ALB validates certificate against provided CA using AWS Certificate Manager Trust Store
3. ALB forwards certificate subject via `x-amzn-mtls-clientcert-subject` header to Lambda function
4. Lambda extracts device serial from certificate subject
5. Lambda looks up device in Iru MDM API by serial number to get user email
6. Lambda authenticates with Okta using OAuth 2.0 JWT bearer token flow against an API integration, using a keypair (not a client secret)
7. Lambda calls Okta Workflow with user email as query parameter and device details as JSON payload
8. Okta Workflow checks if user is eligible and if so, returns TAC to lambda
8. Lambda returns plain text TAC or error message

## Okta Workflow Safeguards

1. User must not be a Super Admin
2. User must be in the `Users: Device Bootstrap` group (cleared nightly)
3. User must be a new hire, defined as: `now()` - `employmentDate` (profile attribute) is >= 0 and <= 7 (i.e., your start date cannot be in the future and cannot be older than 7 days)

## Prerequisites

1. Iru API token stored in AWS Secrets Manager
2. Okta Workflow URL
3. Okta OAuth service integration with public key in JWK format

## Setup

### 1. Configure Okta OAuth Service Integration

1. In Okta Admin Console, go to **Applications** → **Create App Integration**
2. Select **API Services** for the application type  
3. Give it a name like "New Hire TAC Generator"
4. Note the **Client ID** (e.g., `0oa12qgvzx92ZbuKt698`)
5. Go to **Sign On** tab and click **Add Key**
6. Generate JWK format using: `go run okta_jwk.go okta_private_key.pem`
7. Grant the application the `okta.workflows.invoke.manage` scope
8. Note your Okta domain (e.g., `proof.okta.com`)

### 2. Configure AWS Trust Store (Required for mTLS)

1. Create Trust Store in AWS Certificate Manager
2. Upload CA certificate to Trust Store
3. Configure ALB listener to use Trust Store for client certificate validation

### 3. Environment Variables

Update `lambda-env-update.json` or set environment variables:

```json
{
  "Variables": {
    "IRU_API_URL": "https://proof.api.kandji.io",
    "IRU_API_TOKEN_SECRET_NAME": "new-hire-tac/iru-api-token",
    "OKTA_DOMAIN": "proof.okta.com",
    "OKTA_CLIENT_ID": "0oa12qgvzx92ZbuKt698",
    "OKTA_WORKFLOW_URL": "<from Okta Workflow API Endpoint card>",
    "OKTA_PRIVATE_KEY_SECRET_NAME": "okta-private-key-file-name"
  }
}
```

### Expected Response
- **Success**: Plain text TAC code (e.g., `"ABC123DEF456"`)
- **Not Eligible**: `"User not eligible for TAC"`
- **Error**: `"Error checking eligibility"`

## API Integration Details

### Iru MDM API
- **Endpoint**: `GET /api/v1/devices?serial_number={serial}`
- **Authentication**: `Bearer {token}` (from AWS Secrets Manager)
- **Response Format**: Array of device objects
- **Key Fields**: `device_id`, `user.email`, `serial_number`

### Okta Workflow API
- **Method**: `POST`
- **URL**: `{workflow_url}?email={user_email}`
- **Authentication**: `Bearer {oauth_token}` (JWT-based OAuth 2.0)
- **Response**: Plain text TAC or JSON with eligibility status

### OAuth 2.0 Flow
- **Grant Type**: `client_credentials`
- **Client Assertion**: JWT signed with RSA private key
- **Scope**: `okta.workflows.invoke.manage`
- **Token Endpoint**: `https://{okta_domain}/oauth2/v1/token`

## Monitoring & Debugging

### CloudWatch Logs
```bash
# Follow logs in real-time
aws logs tail /aws/lambda/new-hire-tac-generator --follow

# Get logs from specific time range
aws logs filter-log-events --log-group-name "/aws/lambda/new-hire-tac-generator" --start-time 1640000000000
```

### Common Issues

1. **403 Forbidden / TLS Handshake Error**
   - Ensure certificate subject format: `CN=OktaManagementAttestation for {SERIAL}`

2. **Device not found**
   - Verify device exists in Iru MDM with correct serial number
   - Check Iru API token permissions

3. **OAuth/API Errors**
   - Verify Okta domain and client ID configuration
   - Check that `okta.workflows.invoke.manage` scope is granted
   - Ensure private key in AWS Secrets Manager matches public key in Okta

4. **Workflow Errors**
   - Check workflow URL is correct (format: `https://proof.workflows.okta.com/api/flo/{flow-id}/invoke`)
   - Verify workflow expects email as query parameter
   - Check workflow business logic for user eligibility requirements