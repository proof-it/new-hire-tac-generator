#!/bin/bash

# Generate Okta OAuth keys for New Hire TAC Generator
# Run this script to create the required certificate files
# These files will be automatically ignored by git (.gitignore)

set -e

echo "🔐 Generating Okta OAuth key pair..."

# Generate private key
openssl genrsa -out okta_private_key.pem 2048
echo "✅ Generated okta_private_key.pem"

# Extract public key
openssl rsa -in okta_private_key.pem -pubout -out okta_public_key.pem
echo "✅ Generated okta_public_key.pem"

# Generate JWK format for Okta
echo "🔄 Converting to JWK format..."
go run okta_jwk.go okta_public_key.pem > okta_public_key.jwk
echo "✅ Generated okta_public_key.jwk"

echo "
🎉 Key generation complete!

Next steps:
1. Upload okta_public_key.jwk to your Okta OAuth app (Applications -> Sign On -> Add Key)
2. Store okta_private_key.pem in AWS Secrets Manager:
   aws secretsmanager put-secret-value --secret-id 'new-hire-tac/okta-private-key' --secret-string file://okta_private_key.pem

⚠️  IMPORTANT: These files are automatically ignored by git (.gitignore)
⚠️  Never commit private keys to version control!
"