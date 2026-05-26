# OLM clients import by numeric ID. Look it up via the pangolin_clients
# field on the org (or in the Pangolin web UI).
#
# Note: `secret` cannot be recovered from the Pangolin API after
# creation. After importing, it is set to empty in state. Existing
# olm connectors keep working with their original secret.
terraform import pangolin_client.example <client_id>
