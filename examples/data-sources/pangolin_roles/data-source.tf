data "pangolin_roles" "all" {}

output "role_names" {
  value = [for r in data.pangolin_roles.all.roles : r.name]
}
