variable "aws_region" {
  description = "AWS region"
  type        = string
  default     = "us-west-2"
}

variable "project_name" {
  description = "Project name for resource naming"
  type        = string
  default     = "new-hire-tac"
}

variable "iru_api_url" {
  description = "Iru API base URL"
  type        = string
  default     = "https://proof.api.kandji.io"
}

variable "iru_api_token" {
  description = "Iru API token"
  type        = string
  sensitive   = true
}

variable "okta_workflow_url" {
  description = "Okta Workflow webhook URL"
  type        = string
}

variable "okta_domain" {
  description = "Okta domain (e.g., your-org.okta.com)"
  type        = string
}

variable "okta_client_id" {
  description = "Okta OAuth client ID for service integration"
  type        = string
}

variable "okta_private_key" {
  description = "Private key for Okta OAuth service integration (PEM format)"
  type        = string
  sensitive   = true
}

variable "ssl_certificate_arn" {
  description = "ARN of the SSL certificate for ALB HTTPS listener"
  type        = string
}

variable "ca_certificate_arn" {
  description = "ARN of the CA certificate for mTLS client verification"
  type        = string
}

variable "allowed_cidr_blocks" {
  description = "CIDR blocks allowed to access the ALB"
  type        = list(string)
  default     = ["0.0.0.0/0"]
}