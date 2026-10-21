terraform {
  required_providers {
    pangolin = {
      source  = "stackopshq/pangolin"
      version = "~> 1.6"
    }
  }
}

provider "pangolin" {
  url     = "https://pangolin.example.com"
  api_key = var.pangolin_api_key
  org_id  = "my-org"
}
