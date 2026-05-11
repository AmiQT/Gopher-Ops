# 🛠 Terraform Context: Apa Yang Kita Dah Buat?

Dokumen ni ditulis khas sebagai nota peribadi (cheat sheet) untuk ingat balik segala gila/magic **Infrastructure as Code (IaC)** yang kita dah implement dalam fail-fail kat folder `terraform/`. 

Projek asalnya cuma AI bot (SRE) dalam Go, tapi kita tambah kuasa "Automated Environment Provisioning" pakai Terraform.  

## 1. Konsep Asas (HCL & Docker Provider)
- **Tool yang pakai:** `terraform init` (download provider), `terraform plan` (buat draf perancangan PC kita sebelum ejas code), dan `terraform apply -auto-approve` (bina container betul-betul)
- **The Provider:** Kat fail `main.tf`, kita "tarik" provider nama `kreuzwerker/docker`. Ini ajar Terraform yang dia kena borak dengan Docker Engine kat PC host kita.
- **Fail Utama `main.tf`:** Ini tempat kita mengarah (secara declarative) container apa yang patut NAIK. Kita list "Nginx" as Web Server, dengan "Redis" as Database/Cache.

## 2. Scale & Automate Pakai Variables & Loop (Mid-Level Skill)
Dulu kita hardcode Nginx & Redis satu bijik je, tapi kita upgrade jadi **Dynamic Deployment**! 🚀
- **Fail `variables.tf`:** Tempat kita simpan parameter macam `redis_count = 2` dan port `nginx_port`. 
- **Argumen `count`:** Dekat `main.tf`, kat resource redis kita letak `count = var.redis_count`. Terraform secara automatik loop dan create "gopher-ops-redis-node-1" dan "gopher-ops-redis-node-2". Kita tak payah COPY-PASTE kod tu dua kali! No cap fr fr.

## 3. Persistent State & Networking (Senior-Level Skill)
Macam microservices betul-betul di *production*, kita bina dua "nyawa" yang power kat `main.tf` ni:
- **Custom Docker Network:** Nama dia `gopher_ops_network`. Nginx & Redis 1/2 semua ditarik masuk duduk satu bumbung (isolated). Nginx boleh *ping* Redis direct tanpa IP Address, just guna hostname.
- **Persistent Volume:** Kita tambah resource `docker_volume` khas untuk folder `/data` Redis-redis tadi. Ini buat dia jadi **Stateful Service**. Maksudnya? Kalau container redis tu di kill (Destroy) oleh Terraform atau Bot Gopher-Ops, DATA cache dalam tu MASIH WUJUD.

## 4. "Resit" Deployment pakai Outputs
- **Fail `outputs.tf`:** Selepas deployment siap (Apply complete), Terraform print out dekat terminal port Nginx yang dia pilih (`8080`) dan list array semua hostname redis yang tengah run (`node-1`, `node-2`). Senang untuk operator Go/bot baca!

## 5. Hubung kait Nginx/Redis Ni Dengan Bot Go Kita (The End Goal)
Segala perbuatan dari Terraform (Destroy/Create container, Scaling) akan serta merta di_"detect"_ oleh **Bot Gopher-Ops telegram kita**. 
Ini konsep **Observability (via gopsutil + Docker API)**.
SRE tak perlu manual deploy lab; bot SRE AI (Gemini) ada *full context* pada container-container yang Terraform tolong *spin up*! 🔥🦅

---
*Siap! Nota ini untuk rujukan diri sendiri atau bila disoal siasat waktu interview pasal Terraform flow.*