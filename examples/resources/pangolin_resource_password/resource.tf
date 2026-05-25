resource "pangolin_resource_password" "example" {
  resource_id = pangolin_resource.example.id
  password    = var.resource_password
}
