/*
Copyright 2024.

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

// Package all imports all known provider packages to ensure their init() functions run
// and register their factories with the watcher package.
//
// This package should be imported with a blank identifier:
//
//	import _ "github.com/konflux-ci/kueue-external-admission/pkg/providers/all"
//
// Adding new providers is simple - just add their import here:
//  1. Create your provider package: pkg/providers/mynewprovider/
//  2. Implement the provider with init() function that calls watcher.RegisterProviderFactory()
//  3. Add the import line below: _ "github.com/konflux-ci/kueue-external-admission/pkg/providers/mynewprovider"
//  4. That's it! No need to modify any other code.
//
// Example of adding a webhook provider:
//
//	_ "github.com/konflux-ci/kueue-external-admission/pkg/providers/webhook"
package all

import (
	// Import all provider packages to register their factories
	_ "github.com/konflux-ci/kueue-external-admission/pkg/providers/alertmanager"
	// Future providers can be added here:
	// _ "github.com/konflux-ci/kueue-external-admission/pkg/providers/webhook"
	// _ "github.com/konflux-ci/kueue-external-admission/pkg/providers/customcheck"
)
