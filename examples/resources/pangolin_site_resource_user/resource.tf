resource "pangolin_site_resource_user" "example" {
  site_resource_id = pangolin_site_resource.example.id
  user_id          = pangolin_user.example.id
}
