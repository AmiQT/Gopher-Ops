terraform {
  required_providers {
    docker = {
      source  = "kreuzwerker/docker"
      version = "~> 3.0.1"
    }
  }
}

provider "docker" {}

# Tarik image Nginx
resource "docker_image" "nginx" {
  name         = "nginx:latest"
  keep_locally = false
}

# Buat Custom Network (Microservices)
resource "docker_network" "gopher_net" {
  name = "gopher_ops_network"
}

# Run container Nginx
resource "docker_container" "nginx_lab" {
  image = docker_image.nginx.image_id
  name  = var.nginx_container_name
  ports {
    internal = 80
    external = var.nginx_port
  }
  networks_advanced {
    name = docker_network.gopher_net.name
  }
}

# Tarik image Redis
resource "docker_image" "redis" {
  name         = "redis:alpine"
  keep_locally = false
}

# Buat Persistent Volume untuk setiap Redis node
resource "docker_volume" "redis_data" {
  count = var.redis_count
  name  = "gopher_ops_redis_data_${count.index + 1}"
}

# Run container Redis (Guna argumen 'count' untuk scaling)
resource "docker_container" "redis_lab" {
  count = var.redis_count
  image = docker_image.redis.image_id
  name  = "gopher-ops-redis-node-${count.index + 1}"

  networks_advanced {
    name = docker_network.gopher_net.name
  }

  volumes {
    container_path = "/data"
    volume_name    = docker_volume.redis_data[count.index].name
  }
}
