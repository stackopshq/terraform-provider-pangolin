resource "pangolin_site_resource_role" "example" {
  site_resource_id = pangolin_site_resource.example.id
  role_id          = pangolin_role.example.id
}
