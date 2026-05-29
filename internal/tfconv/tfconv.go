// Package tfconv groups the small helpers that convert Go pointer
// types to Terraform Plugin Framework primitives, mapping nil to TF
// null. These were originally duplicated across nearly every file
// under internal/datasources and internal/resources under a half-
// dozen different names; centralizing them removes a recurring
// onboarding trap (which spelling? which package?) and makes the
// null mapping a single source of truth.
package tfconv

import "github.com/hashicorp/terraform-plugin-framework/types"

// StringFromPtr returns types.StringNull() when p is nil, otherwise
// types.StringValue(*p). Use when the upstream JSON tag is a nullable
// string the wire emits as `null` rather than `""`.
func StringFromPtr(p *string) types.String {
	if p == nil {
		return types.StringNull()
	}
	return types.StringValue(*p)
}

// BoolFromPtr returns types.BoolNull() when p is nil, otherwise
// types.BoolValue(*p).
func BoolFromPtr(p *bool) types.Bool {
	if p == nil {
		return types.BoolNull()
	}
	return types.BoolValue(*p)
}

// Int64FromIntPtr returns types.Int64Null() when p is nil, otherwise
// types.Int64Value(int64(*p)). Use when the upstream Go field is
// `*int`. Many Pangolin endpoints emit numeric IDs as nullable JSON
// integers; the helper folds the cast and the null check together.
func Int64FromIntPtr(p *int) types.Int64 {
	if p == nil {
		return types.Int64Null()
	}
	return types.Int64Value(int64(*p))
}

// Int64FromInt64Ptr is the same as Int64FromIntPtr but for `*int64`.
// Distinguished from Int64FromIntPtr at the type level — Go's
// integer-type erasure won't let us write a single function. Kept
// separate rather than introducing generics so the call sites stay
// trivially greppable.
func Int64FromInt64Ptr(p *int64) types.Int64 {
	if p == nil {
		return types.Int64Null()
	}
	return types.Int64Value(*p)
}

// Float64FromPtr returns types.Float64Null() when p is nil, otherwise
// types.Float64Value(*p). Used for the bandwidth counters returned
// by /org/{org}/user-devices and similar.
func Float64FromPtr(p *float64) types.Float64 {
	if p == nil {
		return types.Float64Null()
	}
	return types.Float64Value(*p)
}
