resource "pangolin_site_resource_client" "example" {
  site_resource_id = pangolin_site_resource.example.id
  client_id        = pangolin_client.example.id
}
