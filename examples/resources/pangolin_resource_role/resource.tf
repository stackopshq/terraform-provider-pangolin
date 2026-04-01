resource "pangolin_resource_role" "example" {
  resource_id = pangolin_resource.example.id
  role_id     = pangolin_role.example.id
}
