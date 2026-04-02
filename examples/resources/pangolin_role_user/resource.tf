resource "pangolin_role_user" "example" {
  role_id = pangolin_role.example.id
  user_id = pangolin_user.example.user_id
}
