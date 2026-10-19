output "ecs_cluster_name" {
  description = "Name of the created ECS cluster"
  value       = module.ecs.cluster_name
}

output "ecs_service_name" {
  description = "Name of the running ECS service"
  value       = module.ecs.service_name
}
output "ecr_repository_url" {
  description = "ECR repository the Product API image is pushed to"
  value       = module.ecr.repository_url
}
