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

package llm

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/cli-runtime/pkg/genericclioptions"
)

func TestNewLLMCmd_UseAndShort(t *testing.T) {
	cf := genericclioptions.NewConfigFlags(true)
	cmd := NewLLMCmd(cf)
	assert.Equal(t, "llm", cmd.Use)
	assert.NotEmpty(t, cmd.Short)
}

func TestNewLLMCmd_HasExpectedSubcommands(t *testing.T) {
	cf := genericclioptions.NewConfigFlags(true)
	cmd := NewLLMCmd(cf)

	expected := []string{"svc", "model", "config", "generate", "benchmark"}
	names := make([]string, 0, len(cmd.Commands()))
	for _, sub := range cmd.Commands() {
		names = append(names, sub.Name())
	}

	for _, want := range expected {
		require.Contains(t, names, want, "expected subcommand %q to be registered", want)
	}
}

func TestNewLLMCmd_SubcommandCount(t *testing.T) {
	cf := genericclioptions.NewConfigFlags(true)
	cmd := NewLLMCmd(cf)
	assert.Equal(t, 5, len(cmd.Commands()))
}

func TestNewLLMCmd_SVCSubcommand_Exists(t *testing.T) {
	cf := genericclioptions.NewConfigFlags(true)
	cmd := NewLLMCmd(cf)

	var svcCmd *cobra.Command
	for _, sub := range cmd.Commands() {
		if sub.Name() == "svc" {
			svcCmd = sub
			break
		}
	}
	require.NotNil(t, svcCmd)

	// svc should have run, list, delete, chat subcommands
	expectedSVCSubs := []string{"run", "list", "delete", "chat"}
	svcNames := make([]string, 0, len(svcCmd.Commands()))
	for _, sub := range svcCmd.Commands() {
		svcNames = append(svcNames, sub.Name())
	}
	for _, want := range expectedSVCSubs {
		require.Contains(t, svcNames, want, "expected svc subcommand %q to be registered", want)
	}
}

func TestNewLLMCmd_ModelSubcommand_Exists(t *testing.T) {
	cf := genericclioptions.NewConfigFlags(true)
	cmd := NewLLMCmd(cf)

	var modelCmd *cobra.Command
	for _, sub := range cmd.Commands() {
		if sub.Name() == "model" {
			modelCmd = sub
			break
		}
	}
	require.NotNil(t, modelCmd)

	// model should have list, pull subcommands
	expectedModelSubs := []string{"list", "pull"}
	modelNames := make([]string, 0, len(modelCmd.Commands()))
	for _, sub := range modelCmd.Commands() {
		modelNames = append(modelNames, sub.Name())
	}
	for _, want := range expectedModelSubs {
		require.Contains(t, modelNames, want, "expected model subcommand %q to be registered", want)
	}
}
