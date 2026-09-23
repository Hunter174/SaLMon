package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/hub"
	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/planner"
)

const version = "0.1.0-dev"

type envelope struct {
	SchemaVersion int    `json:"schema_version"`
	Command       string `json:"command"`
	Source        string `json:"source"`
	FetchedAt     string `json:"fetched_at"`
	Data          any    `json:"data"`
}

type modelSummary struct {
	Repository     string   `json:"repository"`
	ResolvedSHA    string   `json:"resolved_sha"`
	Pipeline       string   `json:"pipeline,omitempty"`
	License        string   `json:"license"`
	Downloads      int64    `json:"downloads"`
	Likes          int64    `json:"likes"`
	Gated          any      `json:"gated"`
	GGUFFileCount  int      `json:"gguf_file_count"`
	Classification string   `json:"classification"`
	Purposes       []string `json:"candidate_purposes"`
	Warnings       []string `json:"warnings"`
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		writeJSON(os.Stderr, map[string]any{"schema_version": 1, "error": err.Error()})
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string) error {
	if len(arguments) == 0 {
		return usageError()
	}
	client, err := hub.New(hub.DefaultEndpoint)
	if err != nil {
		return err
	}
	switch arguments[0] {
	case "search":
		flags := flag.NewFlagSet("search", flag.ContinueOnError)
		flags.SetOutput(os.Stderr)
		query := flags.String("query", "", "Hugging Face model search query")
		format := flags.String("format", "any", "any or gguf")
		limit := flags.Int("limit", 20, "maximum results (1-100)")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("search accepts flags only; use --query")
		}
		models, err := client.Search(ctx, hub.SearchOptions{Query: *query, Format: *format, Limit: *limit})
		if err != nil {
			return err
		}
		results := make([]modelSummary, 0, len(models))
		for _, model := range models {
			plan := planner.Build(model)
			results = append(results, modelSummary{
				Repository: model.ID, ResolvedSHA: model.SHA, Pipeline: model.PipelineTag,
				License: plan.License, Downloads: model.Downloads, Likes: model.Likes,
				Gated: gatedValue(model.Gated), GGUFFileCount: len(plan.GGUFFiles),
				Classification: plan.Classification, Purposes: plan.Purposes, Warnings: plan.Warnings,
			})
		}
		return output("search", map[string]any{
			"query": *query, "format": *format, "results": results,
			"persistence": "Search results were fetched live and were not stored by salmon-model.",
		})
	case "inspect", "plan":
		command := arguments[0]
		flags := flag.NewFlagSet(command, flag.ContinueOnError)
		flags.SetOutput(os.Stderr)
		revision := flags.String("revision", "main", "branch, tag, or commit")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		if flags.NArg() != 1 {
			return fmt.Errorf("%s requires one owner/repository argument", command)
		}
		model, err := client.Inspect(ctx, flags.Arg(0), *revision)
		if err != nil {
			return err
		}
		plan := planner.Build(model)
		if command == "plan" {
			return output(command, plan)
		}
		return output(command, map[string]any{
			"repository": model.ID, "resolved_sha": model.SHA, "pipeline": model.PipelineTag,
			"library": model.LibraryName, "downloads": model.Downloads, "likes": model.Likes,
			"gated": gatedValue(model.Gated), "private": model.Private, "disabled": model.Disabled,
			"last_modified": model.LastModified, "license": plan.License,
			"architecture": plan.Architecture, "candidate_purposes": plan.Purposes,
			"gguf": map[string]any{
				"file_count": len(plan.GGUFFiles), "files": plan.GGUFFiles,
				"context_length": ggufContext(model), "parameter_count": ggufParameters(model),
				"has_chat_template": model.GGUF != nil && model.GGUF.ChatTemplate != "",
			},
			"plan":        plan,
			"persistence": "Repository metadata was fetched live and was not stored by salmon-model.",
		})
	case "version", "--version", "-version":
		return output("version", map[string]any{"version": version})
	default:
		return usageError()
	}
}

func output(command string, data any) error {
	return writeJSON(os.Stdout, envelope{
		SchemaVersion: 1, Command: command, Source: "hugging-face-live-api",
		FetchedAt: time.Now().UTC().Format(time.RFC3339), Data: data,
	})
}

func writeJSON(file *os.File, value any) error {
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func gatedValue(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return "unknown"
	}
	return value
}

func ggufContext(model hub.Model) int64 {
	if model.GGUF == nil {
		return 0
	}
	return model.GGUF.ContextLength
}

func ggufParameters(model hub.Model) int64 {
	if model.GGUF == nil {
		return 0
	}
	return model.GGUF.Total
}

func usageError() error {
	return errors.New(strings.TrimSpace(`usage:
  salmon-model search --query TEXT [--format any|gguf] [--limit 20]
  salmon-model inspect OWNER/REPOSITORY [--revision main]
  salmon-model plan OWNER/REPOSITORY [--revision main]
  salmon-model version`))
}
