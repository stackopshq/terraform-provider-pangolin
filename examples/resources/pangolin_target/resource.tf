resource "pangolin_target" "example" {
  resource_id = pangolin_resource.example.id
  site_id     = pangolin_site.example.id
  ip          = "192.168.1.10"
  port        = 8080
  method      = "http"
}
