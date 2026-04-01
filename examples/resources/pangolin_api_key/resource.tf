resource "pangolin_api_key" "example" {
  name = "ci-cd-key"
}

output "api_key_secret" {
  value     = pangolin_api_key.example.secret
  sensitive = true
}
