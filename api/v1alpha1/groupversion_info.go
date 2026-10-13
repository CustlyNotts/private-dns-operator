/*
Copyright 2026 private-dns-operator authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package v1alpha1 contains the dns.custlynotts.io/v1alpha1 API types for
// managing private DNS zones and records in CoreDNS.
// +kubebuilder:object:generate=true
// +groupName=dns.custlynotts.io
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	// GroupVersion is the group version used to register these objects.
	GroupVersion = schema.GroupVersion{Group: "dns.custlynotts.io", Version: "v1alpha1"}

	// SchemeBuilder collects the functions that register this group version's
	// types with a scheme. Each types file adds its own through init.
	//
	// This is apimachinery's builder rather than controller-runtime's, which is
	// deprecated for exactly the reason it names: an API package should be cheap
	// to import, and depending on controller-runtime here drags its whole
	// dependency tree into anyone who only wants the types.
	SchemeBuilder = runtime.NewSchemeBuilder(addGroupVersion)

	// AddToScheme adds the types in this group version to a scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

// addGroupVersion registers the group version itself, which the serialization
// machinery needs alongside the types. controller-runtime's builder did this
// implicitly.
func addGroupVersion(scheme *runtime.Scheme) error {
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}

// registerTypes returns a scheme function adding objects under GroupVersion, so
// each types file keeps registering itself instead of having to be listed here.
func registerTypes(objects ...runtime.Object) func(*runtime.Scheme) error {
	return func(scheme *runtime.Scheme) error {
		scheme.AddKnownTypes(GroupVersion, objects...)
		return nil
	}
}
