variable "nginx_port" {
  description = "Port untuk Nginx expose ke host"
  type        = number
  default     = 8080
}

variable "nginx_container_name" {
  description = "Nama bekas Nginx"
  type        = string
  default     = "gopher-ops-nginx-lab"
}

variable "redis_count" {
  description = "Berapa banyak container Redis nak run?"
  type        = number
  default     = 2
}
