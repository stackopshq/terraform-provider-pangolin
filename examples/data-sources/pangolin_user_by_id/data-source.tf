data "pangolin_user_by_id" "alice" {
  user_id = "39niuxo8j0ji3ok"
}

# Useful for audit / observability outputs.
output "alice_email" {
  value = data.pangolin_user_by_id.alice.email
}

output "alice_is_server_admin" {
  value = data.pangolin_user_by_id.alice.server_admin
}

# Conditional logic based on the originating IDP.
locals {
  alice_is_external = data.pangolin_user_by_id.alice.idp_id != null
}
