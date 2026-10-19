# Wire together four focused modules: network, ecr, logging, ecs.

module "network" {
  source         = "./modules/network"
  service_name   = var.service_name
  container_port = var.container_port
}

module "ecr" {
  source          = "./modules/ecr"
  repository_name = var.ecr_repository_name
}

module "logging" {
  source            = "./modules/logging"
  service_name      = var.service_name
  retention_in_days = var.log_retention_days
}

# Reuse an existing IAM role for ECS tasks
data "aws_iam_role" "lab_role" {
  name = "LabRole"
}

module "ecs" {
  source       = "./modules/ecs"
  service_name = var.service_name
  # Pin to the digest just pushed, so a code change produces a new task
  # definition and ECS rolls out the new image (":latest" alone would not).
  image              = "${module.ecr.repository_url}@${docker_registry_image.app.sha256_digest}"
  container_port     = var.container_port
  subnet_ids         = module.network.subnet_ids
  security_group_ids = [module.network.security_group_id]
  execution_role_arn = data.aws_iam_role.lab_role.arn
  task_role_arn      = data.aws_iam_role.lab_role.arn
  log_group_name     = module.logging.log_group_name
  ecs_count          = var.ecs_count
  region             = var.aws_region
}


// Build & push the Go app image into ECR
locals {
  # Use the URL from the ecr module, and tag it "latest"
  image_name = "${module.ecr.repository_url}:latest"
  # Changes whenever any file in src/ changes
  src_hash = sha1(join("", [for f in sort(fileset("${path.module}/../src", "**")) : filesha1("${path.module}/../src/${f}")]))
}

# Build with the docker CLI rather than the provider's docker_image build:
# the provider's legacy build API sends a corrupted build context to
# Docker Engine 29+ ("failed to read dockerfile: unexpected EOF").
resource "terraform_data" "app_image" {
  triggers_replace = [local.src_hash, local.image_name]

  provisioner "local-exec" {
    # Fargate runs x86_64; --platform keeps builds on Apple Silicon compatible.
    # Single command so it runs the same under cmd.exe, PowerShell and sh.
    command     = "docker build --platform linux/amd64 -t ${local.image_name} ../src"
    working_dir = path.module
  }
}

resource "docker_registry_image" "app" {
  # this will push :latest → ECR (auth comes from provider.tf)
  name       = local.image_name
  depends_on = [terraform_data.app_image]

  # Re-push whenever the source (and so the image) changes
  triggers = {
    src_hash = local.src_hash
  }
}
