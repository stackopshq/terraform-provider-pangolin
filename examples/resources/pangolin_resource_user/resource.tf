resource "pangolin_resource_user" "example" {
  resource_id = pangolin_resource.example.id
  user_id     = pangolin_user.example.id
}
