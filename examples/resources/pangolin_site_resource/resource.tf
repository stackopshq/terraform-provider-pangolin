# L4 tunnel (cidr / host modes) - requires alias + port ranges.
resource "pangolin_site_resource" "internal_db" {
  site_id        = pangolin_site.example.id
  name           = "internal-db"
  mode           = "host"
  destination    = "db.internal"
  alias          = "db.local"
  tcp_port_range = "5432"
}

# L7 HTTP proxy (http mode) - requires domain_id + subdomain + scheme + destination_port.
resource "pangolin_site_resource" "app_proxy" {
  site_id          = pangolin_site.example.id
  name             = "app-proxy"
  mode             = "http"
  destination      = "10.0.0.42"
  domain_id        = data.pangolin_domains.all.domains[0].id
  subdomain        = "app"
  scheme           = "http"
  destination_port = 8080
}

output "app_full_domain" {
  value = pangolin_site_resource.app_proxy.full_domain
}

# SSH-backed site resource (Pangolin 1.19+) - L4 tunnel with PAM push
# notification MFA.
resource "pangolin_site_resource" "ssh_bastion" {
  site_id        = pangolin_site.example.id
  name           = "ssh-bastion"
  mode           = "host"
  destination    = "bastion.internal"
  alias          = "bastion.local"
  tcp_port_range = "22"
  pam_mode       = "push"
}
