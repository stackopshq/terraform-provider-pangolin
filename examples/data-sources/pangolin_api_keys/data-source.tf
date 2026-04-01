data "pangolin_api_keys" "all" {}

output "api_key_names" {
  value = [for k in data.pangolin_api_keys.all.api_keys : k.name]
}
