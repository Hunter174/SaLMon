package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/conversionenv"
	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/convert"
	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/hub"
	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/install"
	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/planner"
	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/quantize"
	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/toolchain"
	"github.com/Hunter174/SaLMon/tools/salmon-model/internal/webui"
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
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
	case "ui":
		flags := flag.NewFlagSet("ui", flag.ContinueOnError)
		flags.SetOutput(os.Stderr)
		noOpen := flags.Bool("no-open", false, "print the local URL without opening a browser")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("ui accepts flags only")
		}
		return webui.Run(ctx, client, webui.Options{OpenBrowser: !*noOpen, Writer: os.Stdout})
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
	case "install-plan":
		flags := flag.NewFlagSet("install-plan", flag.ContinueOnError)
		flags.SetOutput(os.Stderr)
		revision := flags.String("revision", "main", "branch, tag, or commit")
		filename := flags.String("file", "", "exact GGUF repository filename")
		root := flags.String("root", "", "managed storage root")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		if flags.NArg() != 1 || *filename == "" {
			return errors.New("install-plan requires --file and one owner/repository argument")
		}
		model, err := client.Inspect(ctx, flags.Arg(0), *revision)
		if err != nil {
			return err
		}
		plan, err := install.BuildPlan(model, *filename, *root)
		if err != nil {
			return err
		}
		return output("install-plan", plan)
	case "install":
		flags := flag.NewFlagSet("install", flag.ContinueOnError)
		flags.SetOutput(os.Stderr)
		revision := flags.String("revision", "main", "branch, tag, or commit")
		filename := flags.String("file", "", "exact GGUF repository filename")
		root := flags.String("root", "", "managed storage root")
		consent := flags.String("consent", "", "digest from the exact install-plan")
		maximumBytes := flags.Int64("max-bytes", install.DefaultMaximumBytes, "hard download size limit")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		if flags.NArg() != 1 || *filename == "" || *consent == "" {
			return errors.New("install requires --file, --consent, and one owner/repository argument")
		}
		model, err := client.Inspect(ctx, flags.Arg(0), *revision)
		if err != nil {
			return err
		}
		plan, err := install.BuildPlan(model, *filename, *root)
		if err != nil {
			return err
		}
		var lastReported int64
		record, err := install.Execute(ctx, client, plan, *consent, *root, *maximumBytes, func(completed, total int64) {
			if completed-lastReported >= 16<<20 || completed == total {
				_ = writeJSON(os.Stderr, map[string]any{"schema_version": 1, "event": "download_progress", "completed_bytes": completed, "total_bytes": total})
				lastReported = completed
			}
		})
		if err != nil {
			return err
		}
		return outputWithSource("install", "hugging-face-live-api-and-local-storage", record)
	case "remove":
		flags := flag.NewFlagSet("remove", flag.ContinueOnError)
		flags.SetOutput(os.Stderr)
		root := flags.String("root", "", "managed storage root")
		id := flags.String("id", "", "installed model ID from salmon-model list")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 || *id == "" {
			return errors.New("remove requires --id and accepts no positional arguments")
		}
		record, err := install.Remove(*root, *id)
		if err != nil {
			return err
		}
		return outputWithSource("remove", "local-installation-registry", map[string]any{"removed": record})
	case "list":
		flags := flag.NewFlagSet("list", flag.ContinueOnError)
		flags.SetOutput(os.Stderr)
		root := flags.String("root", "", "managed storage root")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("list accepts flags only")
		}
		records, err := install.List(*root)
		if err != nil {
			return err
		}
		return outputWithSource("list", "local-installation-registry", map[string]any{"models": records})
	case "convert-plan":
		flags := flag.NewFlagSet("convert-plan", flag.ContinueOnError)
		flags.SetOutput(os.Stderr)
		revision := flags.String("revision", "main", "branch, tag, or commit")
		outtype := flags.String("outtype", "f16", "f16 or bf16")
		name := flags.String("name", "", "optional managed output filename")
		root := flags.String("root", "", "managed storage root")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		if flags.NArg() != 1 {
			return errors.New("convert-plan requires one owner/repository argument")
		}
		model, err := client.Inspect(ctx, flags.Arg(0), *revision)
		if err != nil {
			return err
		}
		plan, err := convert.BuildPlan(ctx, model, *outtype, *name, *root, convert.Dependencies{})
		if err != nil {
			return err
		}
		return output("convert-plan", plan)
	case "convert":
		flags := flag.NewFlagSet("convert", flag.ContinueOnError)
		flags.SetOutput(os.Stderr)
		revision := flags.String("revision", "main", "branch, tag, or commit")
		outtype := flags.String("outtype", "f16", "f16 or bf16")
		name := flags.String("name", "", "optional managed output filename")
		root := flags.String("root", "", "managed storage root")
		consent := flags.String("consent", "", "digest from the exact convert-plan")
		maximumBytes := flags.Int64("max-source-bytes", convert.DefaultMaximumSourceBytes, "hard aggregate source download limit")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		if flags.NArg() != 1 || *consent == "" {
			return errors.New("convert requires --consent and one owner/repository argument")
		}
		model, err := client.Inspect(ctx, flags.Arg(0), *revision)
		if err != nil {
			return err
		}
		plan, err := convert.BuildPlan(ctx, model, *outtype, *name, *root, convert.Dependencies{})
		if err != nil {
			return err
		}
		lastReported := map[string]int64{}
		record, err := convert.Execute(ctx, client, plan, *consent, *root, *maximumBytes, func(stage, message string, completed, total int64) {
			key := stage + "\n" + message
			if stage == "download" && completed != 0 && completed != total && completed-lastReported[key] < 8<<20 {
				return
			}
			lastReported[key] = completed
			_ = writeJSON(os.Stderr, map[string]any{"schema_version": 1, "event": "conversion_progress", "stage": stage, "message": message, "completed_bytes": completed, "total_bytes": total})
		}, convert.Dependencies{})
		if err != nil {
			return err
		}
		return outputWithSource("convert", "verified-hugging-face-source-pinned-converter-and-managed-storage", record)
	case "conversion-toolchain-plan":
		flags := flag.NewFlagSet("conversion-toolchain-plan", flag.ContinueOnError)
		flags.SetOutput(os.Stderr)
		root := flags.String("root", "", "managed storage root")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("conversion-toolchain-plan accepts flags only")
		}
		plan, err := conversionenv.BuildPlan(*root)
		if err != nil {
			return err
		}
		return outputWithSource("conversion-toolchain-plan", "pinned-local-catalog-and-embedded-lock", plan)
	case "conversion-toolchain-install":
		flags := flag.NewFlagSet("conversion-toolchain-install", flag.ContinueOnError)
		flags.SetOutput(os.Stderr)
		root := flags.String("root", "", "managed storage root")
		consent := flags.String("consent", "", "digest from the exact conversion-toolchain-plan")
		maximumBytes := flags.Int64("max-component-bytes", conversionenv.DefaultMaximumComponentBytes, "hard size limit for each pinned bootstrap artifact")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 || *consent == "" {
			return errors.New("conversion-toolchain-install requires --consent and accepts no positional arguments")
		}
		plan, err := conversionenv.BuildPlan(*root)
		if err != nil {
			return err
		}
		lastReported := map[string]int64{}
		record, err := conversionenv.Execute(ctx, plan, *consent, *root, *maximumBytes, func(stage, message string, completed, total int64) {
			key := stage + "\n" + message
			if stage == "download" && completed != 0 && completed != total && completed-lastReported[key] < 8<<20 {
				return
			}
			lastReported[key] = completed
			_ = writeJSON(os.Stderr, map[string]any{"schema_version": 1, "event": "conversion_toolchain_progress", "stage": stage, "message": message, "completed_bytes": completed, "total_bytes": total})
		}, conversionenv.Dependencies{})
		if err != nil {
			return err
		}
		return outputWithSource("conversion-toolchain-install", "verified-upstream-artifacts-locked-dependencies-and-local-storage", record)
	case "conversion-toolchain-list":
		flags := flag.NewFlagSet("conversion-toolchain-list", flag.ContinueOnError)
		flags.SetOutput(os.Stderr)
		root := flags.String("root", "", "managed storage root")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("conversion-toolchain-list accepts flags only")
		}
		records, err := conversionenv.List(*root)
		if err != nil {
			return err
		}
		return outputWithSource("conversion-toolchain-list", "local-conversion-toolchain-registry", map[string]any{"toolchains": records})
	case "conversion-toolchain-remove":
		flags := flag.NewFlagSet("conversion-toolchain-remove", flag.ContinueOnError)
		flags.SetOutput(os.Stderr)
		root := flags.String("root", "", "managed storage root")
		id := flags.String("id", "", "installed conversion toolchain ID")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 || *id == "" {
			return errors.New("conversion-toolchain-remove requires --id and accepts no positional arguments")
		}
		record, err := conversionenv.Remove(*root, *id)
		if err != nil {
			return err
		}
		return outputWithSource("conversion-toolchain-remove", "local-conversion-toolchain-registry", map[string]any{"removed": record})
	case "quantize-plan":
		flags := flag.NewFlagSet("quantize-plan", flag.ContinueOnError)
		flags.SetOutput(os.Stderr)
		input := flags.String("input", "", "local F16, BF16, or F32 GGUF input")
		preset := flags.String("preset", "", "Q4_K_M, Q5_K_M, or Q8_0")
		name := flags.String("name", "", "optional managed output filename")
		root := flags.String("root", "", "managed storage root")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 || *input == "" || *preset == "" {
			return errors.New("quantize-plan requires --input and --preset and accepts no positional arguments")
		}
		plan, err := quantize.BuildPlan(ctx, *input, *preset, *name, *root, quantize.Dependencies{})
		if err != nil {
			return err
		}
		return outputWithSource("quantize-plan", "local-input-and-pinned-toolchain", plan)
	case "quantize":
		flags := flag.NewFlagSet("quantize", flag.ContinueOnError)
		flags.SetOutput(os.Stderr)
		input := flags.String("input", "", "local F16, BF16, or F32 GGUF input")
		preset := flags.String("preset", "", "Q4_K_M, Q5_K_M, or Q8_0")
		name := flags.String("name", "", "optional managed output filename")
		root := flags.String("root", "", "managed storage root")
		consent := flags.String("consent", "", "digest from the exact quantize-plan")
		maximumBytes := flags.Int64("max-bytes", quantize.DefaultMaximumOutputBytes, "hard generated-output size limit")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 || *input == "" || *preset == "" || *consent == "" {
			return errors.New("quantize requires --input, --preset, and --consent and accepts no positional arguments")
		}
		plan, err := quantize.BuildPlan(ctx, *input, *preset, *name, *root, quantize.Dependencies{})
		if err != nil {
			return err
		}
		record, err := quantize.Execute(ctx, plan, *consent, *root, *maximumBytes, func(message string) {
			_ = writeJSON(os.Stderr, map[string]any{"schema_version": 1, "event": "quantize_progress", "message": message})
		}, quantize.Dependencies{})
		if err != nil {
			return err
		}
		return outputWithSource("quantize", "local-pinned-toolchain-and-managed-storage", record)
	case "toolchain-plan":
		flags := flag.NewFlagSet("toolchain-plan", flag.ContinueOnError)
		flags.SetOutput(os.Stderr)
		root := flags.String("root", "", "managed storage root")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("toolchain-plan accepts flags only")
		}
		plan, err := toolchain.BuildPlan(*root)
		if err != nil {
			return err
		}
		return outputWithSource("toolchain-plan", "pinned-local-catalog", plan)
	case "toolchain-install":
		flags := flag.NewFlagSet("toolchain-install", flag.ContinueOnError)
		flags.SetOutput(os.Stderr)
		root := flags.String("root", "", "managed storage root")
		consent := flags.String("consent", "", "digest from the exact toolchain-plan")
		maximumBytes := flags.Int64("max-bytes", toolchain.DefaultMaximumArchive, "hard archive download size limit")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 || *consent == "" {
			return errors.New("toolchain-install requires --consent and accepts no positional arguments")
		}
		plan, err := toolchain.BuildPlan(*root)
		if err != nil {
			return err
		}
		var lastReported int64
		record, err := toolchain.Execute(ctx, plan, *consent, *root, *maximumBytes, func(completed, total int64) {
			if completed-lastReported >= 1<<20 || completed == total {
				_ = writeJSON(os.Stderr, map[string]any{"schema_version": 1, "event": "toolchain_download_progress", "completed_bytes": completed, "total_bytes": total})
				lastReported = completed
			}
		}, toolchain.Dependencies{})
		if err != nil {
			return err
		}
		return outputWithSource("toolchain-install", "verified-upstream-release-and-local-storage", record)
	case "toolchain-list":
		flags := flag.NewFlagSet("toolchain-list", flag.ContinueOnError)
		flags.SetOutput(os.Stderr)
		root := flags.String("root", "", "managed storage root")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("toolchain-list accepts flags only")
		}
		records, err := toolchain.List(*root)
		if err != nil {
			return err
		}
		return outputWithSource("toolchain-list", "local-toolchain-registry", map[string]any{"toolchains": records})
	case "toolchain-remove":
		flags := flag.NewFlagSet("toolchain-remove", flag.ContinueOnError)
		flags.SetOutput(os.Stderr)
		root := flags.String("root", "", "managed storage root")
		id := flags.String("id", "", "installed toolchain ID from toolchain-list")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 || *id == "" {
			return errors.New("toolchain-remove requires --id and accepts no positional arguments")
		}
		record, err := toolchain.Remove(*root, *id)
		if err != nil {
			return err
		}
		return outputWithSource("toolchain-remove", "local-toolchain-registry", map[string]any{"removed": record})
	case "version", "--version", "-version":
		return outputWithSource("version", "local", map[string]any{"version": version})
	default:
		return usageError()
	}
}

func output(command string, data any) error {
	return outputWithSource(command, "hugging-face-live-api", data)
}

func outputWithSource(command, source string, data any) error {
	return writeJSON(os.Stdout, envelope{
		SchemaVersion: 1, Command: command, Source: source,
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
  salmon-model ui [--no-open]
  salmon-model search --query TEXT [--format any|gguf] [--limit 20]
  salmon-model inspect [--revision main] OWNER/REPOSITORY
  salmon-model plan [--revision main] OWNER/REPOSITORY
  salmon-model install-plan --file FILE [--revision main] [--root PATH] OWNER/REPOSITORY
  salmon-model install --file FILE --consent DIGEST [--revision main] [--root PATH] OWNER/REPOSITORY
  salmon-model list [--root PATH]
  salmon-model remove --id INSTALLATION_ID [--root PATH]
  salmon-model convert-plan [--revision REV] [--outtype f16|bf16] [--name FILE] [--root PATH] OWNER/REPOSITORY
  salmon-model convert --consent DIGEST [--revision REV] [--outtype f16|bf16] [--name FILE] [--root PATH] OWNER/REPOSITORY
  salmon-model conversion-toolchain-plan [--root PATH]
  salmon-model conversion-toolchain-install --consent DIGEST [--root PATH]
  salmon-model conversion-toolchain-list [--root PATH]
  salmon-model conversion-toolchain-remove --id TOOLCHAIN_ID [--root PATH]
  salmon-model quantize-plan --input FILE --preset Q4_K_M|Q5_K_M|Q8_0 [--name FILE] [--root PATH]
  salmon-model quantize --input FILE --preset PRESET --consent DIGEST [--name FILE] [--root PATH]
  salmon-model toolchain-plan [--root PATH]
  salmon-model toolchain-install --consent DIGEST [--root PATH]
  salmon-model toolchain-list [--root PATH]
  salmon-model toolchain-remove --id TOOLCHAIN_ID [--root PATH]
  salmon-model version`))
}
