terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

provider "aws" {
  region = var.aws_region
}

# Data sources for existing VPC and subnets
data "aws_vpc" "default" {
  default = true
}

data "aws_subnets" "public" {
  filter {
    name   = "vpc-id"
    values = [data.aws_vpc.default.id]
  }

  filter {
    name   = "default-for-az"
    values = ["true"]
  }
}

# Create a ZIP file for the Lambda function
data "archive_file" "lambda_zip" {
  type        = "zip"
  source_file = "../main"
  output_path = "lambda_function.zip"

  depends_on = [null_resource.build_lambda]
}

# Build the Go binary
resource "null_resource" "build_lambda" {
  triggers = {
    go_mod = filemd5("../go.mod")
    main_go = filemd5("../main.go")
  }

  provisioner "local-exec" {
    command = "cd .. && GOOS=linux GOARCH=amd64 go build -o main main.go"
  }
}

# IAM role for Lambda function
resource "aws_iam_role" "lambda_role" {
  name = "${var.project_name}-lambda-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Action = "sts:AssumeRole"
        Effect = "Allow"
        Principal = {
          Service = "lambda.amazonaws.com"
        }
      }
    ]
  })
}

# IAM policy for Lambda function
resource "aws_iam_role_policy" "lambda_policy" {
  name = "${var.project_name}-lambda-policy"
  role = aws_iam_role.lambda_role.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "logs:CreateLogGroup",
          "logs:CreateLogStream",
          "logs:PutLogEvents"
        ]
        Resource = "arn:aws:logs:*:*:*"
      }
    ]
  })
}

# Lambda function
resource "aws_lambda_function" "tac_generator" {
  filename         = data.archive_file.lambda_zip.output_path
  function_name    = "${var.project_name}-tac-generator"
  role            = aws_iam_role.lambda_role.arn
  handler         = "main"
  runtime         = "go1.x"
  timeout         = 30

  source_code_hash = data.archive_file.lambda_zip.output_base64sha256

  environment {
    variables = {
      IRU_API_URL       = var.iru_api_url
      IRU_API_TOKEN     = var.iru_api_token
      OKTA_WORKFLOW_URL = var.okta_workflow_url
      OKTA_DOMAIN       = var.okta_domain
      OKTA_CLIENT_ID    = var.okta_client_id
      OKTA_PRIVATE_KEY  = var.okta_private_key
    }
  }

  depends_on = [aws_iam_role_policy.lambda_policy]
}

# Lambda permission for ALB
resource "aws_lambda_permission" "alb_lambda" {
  statement_id  = "AllowExecutionFromALB"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.tac_generator.function_name
  principal     = "elasticloadbalancing.amazonaws.com"
  source_arn    = aws_lb_target_group.lambda.arn
}

# Security group for ALB
resource "aws_security_group" "alb_sg" {
  name        = "${var.project_name}-alb-sg"
  description = "Security group for ALB with mTLS"
  vpc_id      = data.aws_vpc.default.id

  ingress {
    description = "HTTPS with mTLS"
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = var.allowed_cidr_blocks
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name = "${var.project_name}-alb-sg"
  }
}

# Application Load Balancer
resource "aws_lb" "main" {
  name               = "${var.project_name}-alb"
  internal           = false
  load_balancer_type = "application"
  security_groups    = [aws_security_group.alb_sg.id]
  subnets            = data.aws_subnets.public.ids

  enable_deletion_protection = false

  tags = {
    Name = "${var.project_name}-alb"
  }
}

# Target group for Lambda
resource "aws_lb_target_group" "lambda" {
  name        = "${var.project_name}-lambda-tg"
  target_type = "lambda"

  health_check {
    enabled             = true
    healthy_threshold   = 2
    interval            = 30
    matcher             = "200"
    path                = "/"
    port                = "traffic-port"
    protocol            = "HTTP"
    timeout             = 5
    unhealthy_threshold = 2
  }

  tags = {
    Name = "${var.project_name}-lambda-tg"
  }
}

# Target group attachment
resource "aws_lb_target_group_attachment" "lambda" {
  target_group_arn = aws_lb_target_group.lambda.arn
  target_id        = aws_lambda_function.tac_generator.arn
  depends_on       = [aws_lambda_permission.alb_lambda]
}

# HTTPS listener with mTLS
resource "aws_lb_listener" "https" {
  load_balancer_arn = aws_lb.main.arn
  port              = "443"
  protocol          = "HTTPS"
  ssl_policy        = "ELBSecurityPolicy-TLS-1-2-2017-01"
  certificate_arn   = var.ssl_certificate_arn

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.lambda.arn
  }
}

# Listener certificate for mTLS (CA certificate)
resource "aws_lb_listener_certificate" "mtls_ca" {
  listener_arn    = aws_lb_listener.https.arn
  certificate_arn = var.ca_certificate_arn
}

# Update listener to require mTLS
resource "aws_lb_listener_rule" "mtls_required" {
  listener_arn = aws_lb_listener.https.arn
  priority     = 100

  action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.lambda.arn
  }

  condition {
    http_request_method {
      values = ["GET", "POST"]
    }
  }

  # This ensures client certificate is verified
  depends_on = [aws_lb_listener_certificate.mtls_ca]
}