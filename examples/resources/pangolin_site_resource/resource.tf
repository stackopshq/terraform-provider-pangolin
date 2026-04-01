resource "pangolin_site_resource" "example" {
  site_id     = pangolin_site.example.id
  name        = "internal-db"
  mode        = "host"
  destination = "db.internal"
  alias       = "db.local"
  tcp_port_range = "5432"
}
