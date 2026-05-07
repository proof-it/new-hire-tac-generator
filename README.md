# New Hire TAC Generator

A Go Lambda function that generates Temporary Access Codes (TACs) for new hires based on device serial numbers from mTLS client certificates.

## Architecture

```
Client with mTLS cert -> ALB (validates cert) -> Lambda -> Iru API -> Okta Workflow -> TAC
```

1. Client presents mTLS certificate with laptop serial as CN format: `CN=OktaManagementAttestation for {DEVICE_SERIAL}`
2. ALB validates certificate against provided CA using AWS Certificate Manager Trust Store
3. ALB forwards certificate subject via `x-amzn-mtls-clientcert-subject` header to Lambda function
4. Lambda extracts device serial from certificate subject
5. Lambda looks up device in Iru MDM API by serial number to get user email
6. Lambda authenticates with Okta using OAuth 2.0 JWT bearer token flow
7. Lambda calls Okta Workflow with user email as query parameter and device details as JSON payload
8. Lambda returns plain text TAC or error message

## Prerequisites

1. AWS CLI configured with appropriate permissions
2. Go 1.21+ installed  
3. `jq` command-line JSON processor
4. SSL certificate (*.it.proof.com) and CA certificate in AWS Certificate Manager
5. Iru API token stored in AWS Secrets Manager
6. Okta Workflow URL
7. Okta OAuth service integration with public key in JWK format

## Quick Start

### 1. Deploy Infrastructure

```bash
# Check dependencies and configuration
make -f Makefile.aws check-deps
make -f Makefile.aws check-config

# Deploy everything
make -f Makefile.aws deploy
```

### 2. Test the Endpoint

```bash
# Test with client certificate from macOS keychain/Secure Enclave
CURL_SSL_BACKEND=secure-transport /usr/bin/curl --cert "OktaManagementAttestation for DMV6JNFCCP" https://tac.it.proof.com
```

## Detailed Setup

### 1. Configure Okta OAuth Service Integration

1. In Okta Admin Console, go to **Applications** → **Create App Integration**
2. Select **API Services** for the application type  
3. Give it a name like "New Hire TAC Generator"
4. Note the **Client ID** (e.g., `0oa12qgvzx92ZbuKt698`)
5. Go to **Sign On** tab and click **Add Key**
6. Generate JWK format using: `go run okta_jwk.go okta_private_key.pem`
7. Grant the application the `okta.workflows.invoke.manage` scope
8. Note your Okta domain (e.g., `proof.okta.com`)

### 2. Configure Okta Workflow

The workflow must:
- Accept `email` as a query parameter (e.g., `?email=user@domain.com`)
- Accept JSON payload with device details: `{"userEmail": "user@domain.com", "serialNumber": "ABC123", "deviceId": "device-id"}`
- Return either:
  - Plain text TAC (preferred): `"ABC123DEF456"`
  - JSON response: `{"tac": "ABC123DEF", "eligible": true, "message": "User is eligible"}`

### 3. Upload Certificates to AWS Certificate Manager

```bash
# Upload CA certificate for mTLS client verification
make -f Makefile.aws upload-certs

# Note: Server SSL certificate (*.it.proof.com) should already be in ACM
```

### 4. Configure AWS Trust Store (Required for mTLS)

1. Create Trust Store in AWS Certificate Manager
2. Upload CA certificate to Trust Store
3. Configure ALB listener to use Trust Store for client certificate validation

### 5. Environment Variables

Update `lambda-env-update.json` or set environment variables:

```json
{
  "Variables": {
    "IRU_API_URL": "https://proof.api.kandji.io",
    "IRU_API_TOKEN_SECRET_NAME": "new-hire-tac/iru-api-token",
    "OKTA_DOMAIN": "proof.okta.com",
    "OKTA_CLIENT_ID": "0oa12qgvzx92ZbuKt698",
    "OKTA_WORKFLOW_URL": "https://proof.workflows.okta.com/api/flo/d034bb1893473dfdc2d504f32e805a0f/invoke",
    "OKTA_PRIVATE_KEY_SECRET_NAME": "new-hire-tac/okta-private-key"
  }
}
```

## Usage Examples

### Production (macOS Secure Enclave)
```bash
# Find Okta-managed certificates
security find-identity -v -p ssl-client | grep -i okta

# Use certificate from keychain
CURL_SSL_BACKEND=secure-transport /usr/bin/curl --cert "OktaManagementAttestation for DMV6JNFCCP" https://tac.it.proof.com
```

### Development (Certificate Files)
```bash
# If using certificate files instead of keychain
curl --cert client.crt --key client.key --cacert ca.crt https://tac.it.proof.com
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
- **Payload**: `{"userEmail": "user@domain.com", "serialNumber": "ABC123", "deviceId": "device-id"}`
- **Response**: Plain text TAC or JSON with eligibility status

### OAuth 2.0 Flow
- **Grant Type**: `client_credentials`
- **Client Assertion**: JWT signed with RSA private key
- **Scope**: `okta.workflows.invoke.manage`
- **Token Endpoint**: `https://{okta_domain}/oauth2/v1/token`

## Management Commands

```bash
# Update Lambda code after changes
make -f Makefile.aws update-lambda

# Check Lambda function status
aws lambda get-function --function-name new-hire-tac-generator

# View recent logs
aws logs tail /aws/lambda/new-hire-tac-generator --follow

# Get ALB DNS name
aws elbv2 describe-load-balancers --names new-hire-tac-alb --query "LoadBalancers[0].DNSName" --output text

# Destroy all resources
make -f Makefile.aws destroy
```

## Monitoring & Debugging

### CloudWatch Logs
```bash
# Follow logs in real-time
aws logs tail /aws/lambda/new-hire-tac-generator --follow

# Get logs from specific time range
aws logs filter-log-events --log-group-name "/aws/lambda/new-hire-tac-generator" --start-time 1640000000000
```

### Common Log Messages
- `Client certificate subject: CN=OktaManagementAttestation for DMV6JNFCCP` - Certificate parsed successfully
- `Found device: Proof-DMV6JNFCCP assigned to user: john.heasman@proof.com` - Device lookup successful
- `Calling workflow URL: https://proof.workflows.okta.com/api/flo/.../invoke?email=...` - Workflow call initiated
- `Workflow returned plain text TAC: ABC123DEF` - TAC generated successfully

## Security Considerations

1. **mTLS Validation**: Only devices with valid certificates signed by the trusted CA can access the endpoint
2. **Certificate Format**: Strict parsing of certificate subject ensures only Okta-managed devices can authenticate
3. **API Token Security**: Iru API token stored in AWS Secrets Manager with IAM-based access control
4. **OAuth Security**: JWT-based authentication with RSA key signing for Okta API access
5. **Network Security**: ALB can be restricted to specific IP ranges if needed
6. **Audit Trail**: All requests logged in CloudWatch for security monitoring

## Troubleshooting

### Common Issues

1. **403 Forbidden / TLS Handshake Error**
   - Verify client certificate is valid and signed by the correct CA
   - Check Trust Store configuration on ALB
   - Ensure certificate subject format: `CN=OktaManagementAttestation for {SERIAL}`

2. **Device not found**
   - Check device serial number format in certificate
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

### Debug Commands

```bash
# Test certificate parsing locally
echo "CN=OktaManagementAttestation for DMV6JNFCCP" | grep -o 'for [A-Z0-9]*'

# Test Lambda function directly
aws lambda invoke --function-name new-hire-tac-generator --payload fileb://test-event.json /tmp/response.json

# Check AWS Secrets
aws secretsmanager get-secret-value --secret-id "new-hire-tac/iru-api-token" --query "SecretString"
```

## Architecture Details

### Certificate Subject Parsing
The system expects client certificates with the specific format:
```
CN=OktaManagementAttestation for {DEVICE_SERIAL}
```
Where `{DEVICE_SERIAL}` is the device's serial number that matches the Iru MDM record.

### Error Handling
The Lambda function returns appropriate HTTP status codes:
- **200**: TAC generated successfully
- **400**: Invalid certificate format or missing serial
- **403**: User not eligible for TAC
- **404**: Device not found in MDM
- **500**: Internal error (API failures, OAuth issues, etc.)

### Performance
- **Cold Start**: ~100-200ms (Go runtime initialization)
- **Warm Request**: ~1-3 seconds (API calls to Iru + Okta)
- **Memory Usage**: ~35MB peak
- **Timeout**: 30 seconds

## Development

### Local Testing
```bash
# Build and test locally
go build main.go
./main  # Note: Will fail without AWS environment, use for syntax checking

# Test with specific certificate subject
export TEST_CERT_SUBJECT="CN=OktaManagementAttestation for DMV6JNFCCP"
```

### Code Structure
- `main.go`: Complete Lambda handler with all integration logic
- `Makefile.aws`: AWS CLI-based deployment automation
- `okta_jwk.go`: Utility to convert PEM keys to JWK format
- Certificate files: `okta_ca_cert.pem`, `okta_private_key.pem`

## Support

For issues with:
- **AWS Infrastructure**: Check CloudWatch logs and AWS service status
- **Okta Integration**: Verify OAuth configuration and workflow status  
- **Iru MDM**: Check API token and device enrollment status
- **Certificate Issues**: Verify CA trust and certificate format