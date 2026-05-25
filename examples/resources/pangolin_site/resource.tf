resource "pangolin_site" "example" {
  name = "paris-01"
}

output "example_newt_id" {
  value = pangolin_site.example.newt_id
}

output "example_newt_secret" {
  value     = pangolin_site.example.newt_secret
  sensitive = true
}
