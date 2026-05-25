resource "pangolin_resource_header_auth" "example" {
  resource_id            = pangolin_resource.example.id
  user                   = "admin"
  password               = var.header_auth_password
  extended_compatibility = false
}
