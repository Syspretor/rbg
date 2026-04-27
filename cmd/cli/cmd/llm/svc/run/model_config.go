/*
Copyright 2026 The RBG Authors.

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

package run

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	cliconfig "sigs.k8s.io/rbgs/cmd/cli/config"
	"sigs.k8s.io/yaml"
)

// ModelConfig describes a model and its available run modes.
type ModelConfig struct {
	ID    string       `yaml:"id"`
	Name  string       `yaml:"name"`
	Modes []ModeConfig `yaml:"modes"`
}

// ModeConfig describes a single run mode for a model.
type ModeConfig struct {
	Name        string              `yaml:"name"`
	Description string              `yaml:"description"`
	Engine      string              `yaml:"engine"`
	Image       string              `yaml:"image"`
	Resources   corev1.ResourceList `yaml:"resources"`
	Args        []string            `yaml:"args"`
	Env         []corev1.EnvVar     `yaml:"env"`
	Distributed *DistributedConfig  `yaml:"distributed,omitempty"` // Multi-node deployment config
	ShmSize     string              `yaml:"shmSize,omitempty"`     // Shared memory size (e.g., "8Gi", "16Gi")
}

// DistributedConfig describes multi-node deployment configuration.
// Size > 1 enables leader-worker pattern for distributed inference.
type DistributedConfig struct {
	Size int32 `yaml:"size"` // Total nodes (1 leader + N workers)
}

// modelWithSource tracks a model config and its source file
type modelWithSource struct {
	model  ModelConfig
	source string
}

// LoadAllModels loads all available model configurations by merging:
//  1. User-defined models (from the directory returned by GetModelConfigDir(),
//     default: ~/.rbg/models, overridable via RBG_MODEL_CONFIG env var;
//     accepts both .yaml and .yml files)
//  2. Built-in models (embedded models.yaml)
//
// User models are placed before built-in models, so they take precedence during lookup.
// Duplicate detection and warnings are performed during loading.
func LoadAllModels() ([]ModelConfig, error) {
	// 1. Load user-defined models with source tracking
	userModels, err := loadUserModels()
	if err != nil {
		// Log warning but don't fail - user models are optional
		fmt.Fprintf(os.Stderr, "Warning: failed to load user models: %v\n", err)
	}

	// 2. Load built-in models (always available)
	builtinModels, err := loadBuiltinModels()
	if err != nil {
		return nil, fmt.Errorf("failed to load builtin models: %w", err)
	}

	// 3. Detect and warn about duplicates
	detectModelConflicts(userModels, builtinModels)

	// 4. Merge: user models first (higher priority), then builtin models
	allModels := make([]ModelConfig, 0, len(userModels)+len(builtinModels))
	for _, m := range userModels {
		allModels = append(allModels, m.model)
	}
	allModels = append(allModels, builtinModels...)

	return allModels, nil
}

// detectModelConflicts checks for duplicate model definitions and warns appropriately
// Each model ID gets at most one warning, aggregating all definition sources
func detectModelConflicts(userModels []modelWithSource, builtinModels []ModelConfig) {
	// 1. Collect all definitions for each model ID
	// Important: Record user models FIRST (they have higher priority)
	type definition struct {
		source string // source file name
	}

	modelDefinitions := make(map[string][]definition)

	// Record user models first (higher priority)
	for _, um := range userModels {
		modelDefinitions[um.model.ID] = append(modelDefinitions[um.model.ID], definition{
			source: um.source,
		})
	}

	// Then record builtin models
	for _, m := range builtinModels {
		modelDefinitions[m.ID] = append(modelDefinitions[m.ID], definition{
			source: "builtin",
		})
	}

	// 2. Emit aggregated warnings for models with multiple definitions
	// Sort model IDs for deterministic output order
	sortedModelIDs := make([]string, 0, len(modelDefinitions))
	for modelID := range modelDefinitions {
		sortedModelIDs = append(sortedModelIDs, modelID)
	}
	sort.Strings(sortedModelIDs)

	for _, modelID := range sortedModelIDs {
		defs := modelDefinitions[modelID]
		if len(defs) <= 1 {
			continue // Only one definition, no conflict
		}

		// Count definitions per source file
		fileCount := make(map[string]int)
		for _, d := range defs {
			fileCount[d.source]++
		}

		// Sort source files for deterministic output order
		sortedFiles := make([]string, 0, len(fileCount))
		for file := range fileCount {
			sortedFiles = append(sortedFiles, file)
		}
		sort.Strings(sortedFiles)

		// Build warning message parts
		parts := make([]string, 0, len(sortedFiles))
		for _, file := range sortedFiles {
			count := fileCount[file]
			if count == 1 {
				parts = append(parts, fmt.Sprintf("1 in %s", file))
			} else {
				parts = append(parts, fmt.Sprintf("%d in %s", count, file))
			}
		}

		// First definition wins (user models come first)
		firstSource := defs[0].source

		fmt.Fprintf(os.Stderr,
			"Warning: model %q has %d definitions (%s), first definition in %s will be used\n",
			modelID, len(defs), strings.Join(parts, ", "), firstSource)
	}
}

// loadBuiltinModels loads the embedded model configurations
func loadBuiltinModels() ([]ModelConfig, error) {
	var configs []ModelConfig
	if err := yaml.Unmarshal(embeddedModelsYAML, &configs); err != nil {
		return nil, fmt.Errorf("failed to parse builtin model configs: %w", err)
	}
	return configs, nil
}

// loadUserModels loads user-defined models from the models/ directory
// Returns models with their source file information for conflict detection
func loadUserModels() ([]modelWithSource, error) {
	modelsDir := cliconfig.GetModelConfigDir()
	if modelsDir == "" {
		return nil, nil
	}

	envModelsDir, envModelsDirSet := os.LookupEnv("RBG_MODEL_CONFIG")
	// Check if directory exists and is actually a directory
	info, err := os.Stat(modelsDir)
	if os.IsNotExist(err) {
		if envModelsDirSet && envModelsDir != "" && envModelsDir == modelsDir {
			fmt.Fprintf(os.Stderr, "Warning: model config directory %q from RBG_MODEL_CONFIG does not exist, skipping\n", modelsDir)
		}
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to stat models directory: %w", err)
	}
	if !info.IsDir() {
		fmt.Fprintf(os.Stderr, "Warning: models path %q is not a directory, skipping\n", modelsDir)
		return nil, nil
	}

	// Read all YAML files in the directory
	entries, err := os.ReadDir(modelsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read models directory: %w", err)
	}

	// Sort entries by filename for deterministic order
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	var allModels []modelWithSource
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		// Only process .yaml and .yml files (case-insensitive)
		name := entry.Name()
		lowerName := strings.ToLower(name)
		if !(strings.HasSuffix(lowerName, ".yaml") || strings.HasSuffix(lowerName, ".yml")) {
			continue
		}

		filePath := filepath.Join(modelsDir, name)
		data, err := os.ReadFile(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to read model file %s: %v\n", filePath, err)
			continue
		}

		var models []ModelConfig
		if err := yaml.Unmarshal(data, &models); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to parse model file %s: %v\n", filePath, err)
			continue
		}

		// Track source file for each model
		for _, m := range models {
			allModels = append(allModels, modelWithSource{
				model:  m,
				source: name, // Use filename for user-friendly messages
			})
		}
	}

	return allModels, nil
}

// FindModelConfig finds the best matching ModelConfig for modelID using:
//  1. Exact match
//  2. Wildcard match (e.g. "Qwen/*")
//  3. Default config ("*")
func FindModelConfig(models []ModelConfig, modelID string) (*ModelConfig, error) {
	var wildcardMatch *ModelConfig
	var defaultMatch *ModelConfig

	for i := range models {
		mc := &models[i]
		if mc.ID == modelID {
			return mc, nil
		}
		if mc.ID == "*" {
			defaultMatch = mc
			continue
		}
		if matched, _ := path.Match(mc.ID, modelID); matched {
			if wildcardMatch == nil {
				wildcardMatch = mc
			}
		}
	}

	if wildcardMatch != nil {
		return wildcardMatch, nil
	}
	if defaultMatch != nil {
		return defaultMatch, nil
	}

	return nil, fmt.Errorf("no configuration found for model %q", modelID)
}

// FindModeConfig finds a named mode within a ModelConfig.
// If mode is empty, the first mode in the list is used.
func FindModeConfig(mc *ModelConfig, mode string) (*ModeConfig, error) {
	if len(mc.Modes) == 0 {
		return nil, fmt.Errorf("no modes defined for model %q", mc.ID)
	}
	if mode == "" {
		return &mc.Modes[0], nil
	}

	modeNames := make([]string, 0, len(mc.Modes))
	for i := range mc.Modes {
		m := &mc.Modes[i]
		if m.Name == mode {
			return m, nil
		}
		modeNames = append(modeNames, m.Name)
	}

	return nil, fmt.Errorf("mode %q not found for model %q, available modes: %v", mode, mc.ID, modeNames)
}
