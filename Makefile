.PHONY: build clean deploy destroy test deps

# Build the Go binary for Lambda
build:
	GOOS=linux GOARCH=amd64 go build -o bootstrap main.go

# Download Go dependencies
deps:
	go mod tidy
	go mod download

# Test the Go code
test:
	go test -v ./...

# Clean build artifacts
clean:
	rm -f main
	rm -f terraform/lambda_function.zip

# AWS deployment (replaces Terraform)
include Makefile.aws

# Deploy to AWS using CLI
aws-deploy: check-deps check-config
	$(MAKE) -f Makefile.aws deploy

# Destroy AWS resources
aws-destroy:
	$(MAKE) -f Makefile.aws destroy

# Format Go code
fmt:
	go fmt ./...

# Lint Go code (requires golangci-lint)
lint:
	golangci-lint run

# Local development setup
dev-setup: deps init
	cd terraform && cp terraform.tfvars.example terraform.tfvars
	@echo "Please edit terraform/terraform.tfvars with your configuration"

# View Terraform outputs
outputs:
	cd terraform && terraform output

# Generate keypair for Okta OAuth
generate-keys:
	openssl genrsa -out okta_private_key.pem 2048
	openssl rsa -in okta_private_key.pem -pubout -out okta_public_key.pem
	@echo "Keys generated. Convert to JWK format:"
	@echo "go run convert_key_to_jwk.go okta_public_key.pem"

# Convert public key to JWK format for Okta
jwk:
	@echo "=== Okta JWK Format ==="
	go run okta_jwk.go okta_public_key.pem