# List the roles that currently have access to an HTTP resource.
data "pangolin_resource_roles" "app" {
  resource_id = pangolin_resource.app.id
}

output "role_names" {
  value = [for r in data.pangolin_resource_roles.app.roles : r.name]
}

# Detect whether the admin role is bound to the resource.
output "admin_role_attached" {
  value = anytrue([
    for r in data.pangolin_resource_roles.app.roles : r.is_admin
  ])
}
