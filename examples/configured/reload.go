package main

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"slices"
	"strings"

	"github.com/maxtanforge/go-frame/config/reload"
)

func cloneSettings(c settings) settings {
	c.Business.Labels = slices.Clone(c.Business.Labels)
	return c
}

// applyLevel must be a bounded, non-failing assignment. The real logger accepts
// every level allowed by settings.Validate; no resource replacement occurs here.
func newSettingsManager(initial settings, applyLevel func(string)) (*reload.Manager[settings], error) {
	return reload.New(initial, reload.Options[settings]{Clone: cloneSettings, Validate: func(c settings) error { return c.Validate() }, Diff: func(current, candidate settings) []string {
		paths := []string{}
		if current.Framework.Log.Level != candidate.Framework.Log.Level {
			paths = append(paths, "log.level")
		}
		if current.Business.Message != candidate.Business.Message {
			paths = append(paths, "business.message")
		}
		if !slices.Equal(current.Business.Labels, candidate.Business.Labels) {
			paths = append(paths, "business.labels")
		}
		return paths
	}, Prepare: func(current, candidate settings) (func(), error) {
		left, right := cloneSettings(current), cloneSettings(candidate)
		left.Framework.Log.Level, right.Framework.Log.Level = "", ""
		left.Business.Message, right.Business.Message = "", ""
		left.Business.Labels, right.Business.Labels = nil, nil
		if !reflect.DeepEqual(left, right) {
			return nil, reload.ErrRestartRequired
		}
		return func() {
			if applyLevel != nil {
				applyLevel(candidate.Framework.Log.Level)
			}
		}, nil
	}})
}

func reloadSettings(ctx context.Context, in *settingsInputs, m *reload.Manager[settings]) ([]string, error) {
	candidate, err := in.Load()
	if err != nil {
		return nil, errors.New("configured: configuration load rejected")
	}
	if reflect.DeepEqual(m.Snapshot().Value, candidate.Value) {
		return nil, nil
	}
	_, paths, err := m.Update(ctx, candidate.Value)
	return paths, err
}

func watchSettings(ctx context.Context, in *settingsInputs, m *reload.Manager[settings], lg *slog.Logger) error {
	return reload.Watch(ctx, reload.WatchOptions{Paths: in.Paths(), OnChange: func(ctx context.Context) error {
		paths, err := reloadSettings(ctx, in, m)
		if err != nil {
			reason := "invalid_configuration"
			if errors.Is(err, reload.ErrRestartRequired) {
				reason = "restart_required"
			}
			lg.WarnContext(ctx, "configuration reload rejected", "config_version", m.Snapshot().Version, "reason", reason)
			return nil
		}
		if len(paths) > 0 {
			lg.InfoContext(ctx, "configuration reloaded", "config_version", m.Snapshot().Version, "paths", strings.Join(paths, ","))
		}
		return nil
	}, OnError: func(error) {
		lg.WarnContext(ctx, "configuration watch failed", "config_version", m.Snapshot().Version, "reason", "file_unavailable")
	}})
}
