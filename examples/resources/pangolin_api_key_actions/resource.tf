resource "pangolin_api_key" "ci_bot" {
  name = "ci-bot"
}

resource "pangolin_api_key_actions" "ci_bot" {
  api_key_id = pangolin_api_key.ci_bot.id
  actions = [
    "getOrg",
    "listSites",
    "listResources",
  ]
}
