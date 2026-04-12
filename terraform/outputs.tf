output "nginx_port_dipakai" {
  description = "Port Nginx yang boleh diakses dari browser"
  value       = docker_container.nginx_lab.ports[0].external
}

output "senarai_redis" {
  description = "Senarai nama container Redis yang sedang berjalan"
  value       = [for container in docker_container.redis_lab : container.name]
}
