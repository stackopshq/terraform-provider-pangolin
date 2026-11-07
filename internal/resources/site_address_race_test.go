package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/stackopshq/terraform-provider-pangolin/internal/client"
)

// TestSiteCreate_ParallelGetDistinctAddresses mimics the server: the pick
// hands out the next unused address, and a create with a used one is a 409.
// Without the address lock, parallel creates pick the same address.
func TestSiteCreate_ParallelGetDistinctAddresses(t *testing.T) {
	var mu sync.Mutex
	used := map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case http.MethodGet: // pick-site-defaults
			addr := fmt.Sprintf("100.90.128.%d", len(used)+1)
			fmt.Fprintf(w, `{"success":true,"data":{"newtId":"n","newtSecret":"s","clientAddress":%q}}`, addr)
		case http.MethodPut: // create site
			var body client.CreateSiteRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			// Let the other creates reach the pick before this one lands.
			mu.Unlock()
			time.Sleep(20 * time.Millisecond)
			mu.Lock()
			if used[body.Address] {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"success":false,"message":"Subnet already exists in sites","data":null}`))
				return
			}
			used[body.Address] = true
			fmt.Fprintf(w, `{"success":true,"data":{"siteId":%d,"name":%q,"type":"newt","address":%q}}`, len(used), body.Name, body.Address)
		}
	}))
	t.Cleanup(srv.Close)

	r := &SiteResource{client: client.NewClient(srv.URL, "k", "org")}
	ctx := context.Background()
	var sresp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sresp)

	var wg sync.WaitGroup
	errs := make(chan string, 4)
	for i := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			plan := tfsdk.Plan{Schema: sresp.Schema, Raw: tftypes.NewValue(sresp.Schema.Type().TerraformType(ctx), nil)}
			if d := plan.Set(ctx, &SiteResourceModel{
				ID: types.Int64Unknown(), NiceID: types.StringUnknown(), Name: types.StringValue(fmt.Sprintf("s%d", i)),
				Type: types.StringUnknown(), Online: types.BoolUnknown(), Address: types.StringUnknown(),
				NewtID: types.StringUnknown(), NewtSecret: types.StringUnknown(), DockerSocketEnabled: types.BoolUnknown(),
			}); d.HasError() {
				errs <- fmt.Sprint(d)
				return
			}
			resp := resource.CreateResponse{State: tfsdk.State{Schema: sresp.Schema}}
			r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
			if resp.Diagnostics.HasError() {
				errs <- fmt.Sprint(resp.Diagnostics)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}
