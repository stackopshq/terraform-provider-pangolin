resource "pangolin_resource_whitelist" "example" {
  resource_id = pangolin_resource.example.id
  email       = "alice@example.com"
}
