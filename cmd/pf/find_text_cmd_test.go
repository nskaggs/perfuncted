package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"testing"

	"github.com/nskaggs/perfuncted/v2"
	"github.com/nskaggs/perfuncted/v2/find"
	"github.com/nskaggs/perfuncted/v2/pftest"
)

type commandTextRecognizer struct{ words []find.OCRWord }

func (*commandTextRecognizer) Name() string     { return "fixture" }
func (*commandTextRecognizer) Language() string { return "eng" }
func (r *commandTextRecognizer) Recognize(context.Context, image.Image) ([]find.OCRWord, error) {
	return r.words, nil
}

func TestFindTextCLIJSONUsesExplicitProviderAndScreenCoordinates(t *testing.T) {
	region := image.Rect(0, 0, 4, 2)
	frame := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	provider := &commandTextRecognizer{words: []find.OCRWord{
		{Text: "Continue", PixelRect: image.Rect(0, 0, 2, 1), Page: 1, Block: 1, Paragraph: 1, Line: 1, Word: 1},
		{Text: "Continue", PixelRect: image.Rect(2, 1, 4, 2), Page: 1, Block: 1, Paragraph: 1, Line: 2, Word: 1},
	}}
	session := perfuncted.NewSessionForTesting(&verifiedTemplateTestScreenshotter{Screenshotter: &pftest.Screenshotter{Frames: []image.Image{frame}}}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	openCalls := 0
	open := func(context.Context) (*perfuncted.Session, error) { openCalls++; return session, nil }
	factory := func(executable, language string) (find.OCRRecognizer, error) {
		if executable != "" || language != "eng" {
			t.Fatalf("factory executable=%q language=%q", executable, language)
		}
		return provider, nil
	}
	cmd := findTextCmd(open, factory)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--query", "Continue", "--rect", "0,0,4,2", "--provider", "tesseract", "--output", "json"})
	var output bytes.Buffer
	cmd.SetOut(&output)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var got find.TextResult
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatalf("JSON %q: %v", output.String(), err)
	}
	if openCalls != 1 || got.Provider != "fixture" || got.SourceRect != region || len(got.Matches) != 2 || got.Matches[0].Rect != image.Rect(0, 0, 2, 1) || got.Matches[1].Rect != image.Rect(2, 1, 4, 2) {
		t.Fatalf("openCalls=%d result=%+v", openCalls, got)
	}
}

func TestFindTextCLIMissingProviderAndBadRequestsDoNotOpenSession(t *testing.T) {
	openCalls := 0
	open := func(context.Context) (*perfuncted.Session, error) {
		openCalls++
		return nil, errors.New("must not open")
	}
	missing := func(string, string) (find.OCRRecognizer, error) { return nil, find.ErrOCRProviderUnavailable }
	cmd := findTextCmd(open, missing)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--query", "Continue", "--rect", "0,0,1,1", "--provider", "tesseract"})
	var output bytes.Buffer
	cmd.SetOut(&output)
	if err := cmd.Execute(); !errors.Is(err, find.ErrOCRProviderUnavailable) || openCalls != 0 || output.Len() != 0 {
		t.Fatalf("missing provider err=%v open=%d out=%q", err, openCalls, output.String())
	}

	cmd = findTextCmd(open, func(string, string) (find.OCRRecognizer, error) { return &commandTextRecognizer{}, nil })
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--query", "Continue", "--rect", "0,0,1,1", "--provider", "remote"})
	if err := cmd.Execute(); err == nil || openCalls != 0 {
		t.Fatalf("unsupported provider err=%v open=%d", err, openCalls)
	}
}

func TestFindTextCLIPlainNoMatchIsSuccess(t *testing.T) {
	region := image.Rect(0, 0, 2, 2)
	session := perfuncted.NewSessionForTesting(&verifiedTemplateTestScreenshotter{Screenshotter: &pftest.Screenshotter{Frames: []image.Image{image.NewNRGBA(region)}}}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	provider := &commandTextRecognizer{}
	cmd := findTextCmd(func(context.Context) (*perfuncted.Session, error) { return session, nil }, func(string, string) (find.OCRRecognizer, error) { return provider, nil })
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--query", "missing", "--rect", "0,0,2,2", "--provider", "tesseract"})
	var output bytes.Buffer
	cmd.SetOut(&output)
	if err := cmd.Execute(); err != nil || output.String() != "no match\n" {
		t.Fatalf("err=%v output=%q", err, output.String())
	}
}
