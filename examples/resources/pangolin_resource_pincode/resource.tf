resource "pangolin_resource_pincode" "example" {
  resource_id = pangolin_resource.example.id
  pincode     = "123456"
}
