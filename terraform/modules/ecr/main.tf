# Create (or ensure) an ECR repo exists
resource "aws_ecr_repository" "this" {
  name = var.repository_name
  # Let terraform destroy delete the repo even though it still holds images
  force_delete = true
  # You can add lifecycle_policy, scan_on_push, etc., here
}
