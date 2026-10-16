package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

type dshModelSelection struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Effort   string `json:"reasoningEffort,omitempty"`
}

func (s dshModelSelection) value() string {
	if s.Provider == "" {
		return s.Model
	}
	return url.PathEscape(s.Provider) + "/" + url.PathEscape(s.Model)
}

type dshModelCatalog struct {
	Default dshModelSelection `json:"default"`
	Groups  []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Models []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
			Reasoning   *struct {
				DefaultEffort string `json:"defaultEffort"`
				Efforts       []struct {
					ID          string `json:"id"`
					Description string `json:"description"`
				} `json:"efforts"`
			} `json:"reasoning"`
		} `json:"models"`
	} `json:"groups"`
	Failures []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"failures"`
}

func (b *dshChatBackend) modelCatalog(ctx context.Context, client *dshClient) (dshModelCatalog, error) {
	value, err := client.call(ctx, "session/modelCatalog", nil)
	if err != nil {
		return dshModelCatalog{}, b.translateCallError(err)
	}
	var catalog dshModelCatalog
	if json.Unmarshal(value, &catalog) != nil || catalog.Groups == nil {
		return dshModelCatalog{}, fmt.Errorf("%w: modelCatalog 未返回 groups", errDSHProtocolChanged)
	}
	seen := make(map[string]bool)
	for _, group := range catalog.Groups {
		if group.ID == "" || group.Models == nil {
			return dshModelCatalog{}, fmt.Errorf("%w: 无效模型提供商", errDSHProtocolChanged)
		}
		for _, model := range group.Models {
			key := (dshModelSelection{Provider: group.ID, Model: model.ID}).value()
			if model.ID == "" || seen[key] {
				return dshModelCatalog{}, fmt.Errorf("%w: 无效或重复的模型身份", errDSHProtocolChanged)
			}
			seen[key] = true
		}
	}
	return catalog, nil
}

func (c dshModelCatalog) options() []ChatModelOption {
	options := make([]ChatModelOption, 0)
	for _, group := range c.Groups {
		providerName := group.Name
		if providerName == "" {
			providerName = group.ID
		}
		for _, model := range group.Models {
			name := model.Name
			if name == "" {
				name = model.ID
			}
			selection := dshModelSelection{Provider: group.ID, Model: model.ID}
			option := ChatModelOption{Value: selection.value(), Provider: group.ID, Model: model.ID,
				DisplayName: name + " · " + providerName, Description: model.Description,
				IsDefault: c.Default.Provider == group.ID && c.Default.Model == model.ID}
			if model.Reasoning != nil {
				option.DefaultEffort = model.Reasoning.DefaultEffort
				for _, effort := range model.Reasoning.Efforts {
					option.SupportedEfforts = append(option.SupportedEfforts, ChatReasoningEffortOption{Value: effort.ID, Description: effort.Description})
				}
			}
			options = append(options, option)
		}
	}
	return options
}

func (c dshModelCatalog) warning() string {
	names := make([]string, 0, len(c.Failures))
	for _, failure := range c.Failures {
		name := failure.Name
		if name == "" {
			name = failure.ID
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		for _, group := range c.Groups {
			if len(group.Models) > 0 {
				return ""
			}
		}
		return "DSH 当前没有可用模型"
	}
	return "以下提供商的模型目录不可用：" + strings.Join(names, "、")
}

func (b *dshChatBackend) sessionSelections(ctx context.Context, client *dshClient, sessionID string) (last, next dshModelSelection, err error) {
	value, err := client.call(ctx, "session/list", map[string]any{"_request": map[string]any{}})
	if err != nil {
		return last, next, b.translateCallError(err)
	}
	b.syncQuestionListing(value)
	var listing struct {
		Items []struct {
			SessionID   string `json:"sessionId"`
			Projections struct {
				Values struct {
					ModelSelection struct {
						LastUsed dshModelSelection `json:"lastUsed"`
						Next     dshModelSelection `json:"next"`
					} `json:"modelSelection"`
				} `json:"values"`
			} `json:"projections"`
		} `json:"items"`
	}
	if json.Unmarshal(value, &listing) != nil || listing.Items == nil {
		return last, next, fmt.Errorf("%w: session/list 未返回 items", errDSHProtocolChanged)
	}
	for _, item := range listing.Items {
		if item.SessionID == sessionID {
			return item.Projections.Values.ModelSelection.LastUsed, item.Projections.Values.ModelSelection.Next, nil
		}
	}
	return last, next, nil
}

func (b *dshChatBackend) selectModel(ctx context.Context, client *dshClient, sessionID string, opts ChatTurnOptions) error {
	if opts.Model == "" && opts.Effort == "" {
		return nil
	}
	catalog, err := b.modelCatalog(ctx, client)
	if err != nil {
		return err
	}
	modelValue := opts.Model
	if modelValue == "" {
		last, next, err := b.sessionSelections(ctx, client, sessionID)
		if err != nil {
			return err
		}
		if next.Model != "" {
			modelValue = next.value()
		} else if last.Model != "" {
			modelValue = last.value()
		} else {
			modelValue = catalog.Default.value()
		}
	}
	var selected *ChatModelOption
	for _, option := range catalog.options() {
		if option.Value == modelValue {
			selected = &option
			break
		}
	}
	if selected == nil {
		return fmt.Errorf("DSH 模型目录中没有该选项：%s", modelValue)
	}
	request := map[string]any{"sessionId": sessionID, "provider": selected.Provider, "model": selected.Model}
	if opts.Effort != "" {
		request["reasoningEffort"] = opts.Effort
	}
	value, err := client.call(ctx, "session/selectModel", map[string]any{"request": request})
	if err != nil {
		return b.translateCallError(err)
	}
	var response struct {
		Selected dshModelSelection `json:"selected"`
	}
	if json.Unmarshal(value, &response) != nil || response.Selected.Provider != selected.Provider || response.Selected.Model != selected.Model {
		return fmt.Errorf("%w: selectModel 未确认所选模型", errDSHProtocolChanged)
	}
	return nil
}
