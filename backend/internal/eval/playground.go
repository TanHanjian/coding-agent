package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const (
	PromptPlaygroundVersion = 1
	InterviewPromptKey      = "interview-review-agent"
)

type PromptPlaygroundManifest struct {
	Version   int                        `json:"version"`
	PromptKey string                     `json:"promptKey"`
	Variables []PromptVariableSpec       `json:"variables"`
	Scenarios []PromptPlaygroundScenario `json:"scenarios"`
}

type PromptVariableSpec struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description"`
	Example     any    `json:"example"`
}

type PromptPlaygroundScenario struct {
	ID      string   `json:"id"`
	Purpose string   `json:"purpose"`
	CaseIDs []string `json:"caseIds"`
}

var interviewPromptVariables = map[string]string{
	"history":              "placeholder",
	"query":                "string",
	"interview_context":    "string",
	"conversation_summary": "string",
}

func LoadPromptPlaygroundFile(path string) (PromptPlaygroundManifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return PromptPlaygroundManifest{}, err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var manifest PromptPlaygroundManifest
	if err := decoder.Decode(&manifest); err != nil {
		return PromptPlaygroundManifest{}, fmt.Errorf("invalid Prompt Playground manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return PromptPlaygroundManifest{}, err
	}
	return manifest, nil
}

func (m PromptPlaygroundManifest) Validate() error {
	if m.Version != PromptPlaygroundVersion {
		return fmt.Errorf("unsupported Prompt Playground version %d", m.Version)
	}
	if strings.TrimSpace(m.PromptKey) == "" {
		return fmt.Errorf("Prompt Playground promptKey is required")
	}
	if strings.TrimSpace(m.PromptKey) != InterviewPromptKey {
		return fmt.Errorf("Prompt Playground promptKey must be %q", InterviewPromptKey)
	}
	if len(m.Variables) == 0 {
		return fmt.Errorf("Prompt Playground variables are required")
	}
	seenVariables := make(map[string]struct{}, len(m.Variables))
	for _, variable := range m.Variables {
		name := strings.TrimSpace(variable.Name)
		if name == "" {
			return fmt.Errorf("Prompt Playground variable name is required")
		}
		if _, exists := seenVariables[name]; exists {
			return fmt.Errorf("duplicate Prompt Playground variable %q", name)
		}
		seenVariables[name] = struct{}{}
		expectedType, supported := interviewPromptVariables[name]
		if !supported {
			return fmt.Errorf("unsupported interview Prompt variable %q", name)
		}
		if variable.Type != expectedType {
			return fmt.Errorf("Prompt variable %q has type %q, want %q", name, variable.Type, expectedType)
		}
		if !variable.Required {
			return fmt.Errorf("Prompt variable %q must be required", name)
		}
	}
	if len(seenVariables) != len(interviewPromptVariables) {
		return fmt.Errorf("Prompt Playground must define exactly the interview Prompt variables")
	}

	if len(m.Scenarios) == 0 {
		return fmt.Errorf("Prompt Playground scenarios are required")
	}
	seenScenarios := make(map[string]struct{}, len(m.Scenarios))
	for _, scenario := range m.Scenarios {
		id := strings.TrimSpace(scenario.ID)
		if id == "" {
			return fmt.Errorf("Prompt Playground scenario id is required")
		}
		if _, exists := seenScenarios[id]; exists {
			return fmt.Errorf("duplicate Prompt Playground scenario %q", id)
		}
		seenScenarios[id] = struct{}{}
		if strings.TrimSpace(scenario.Purpose) == "" {
			return fmt.Errorf("Prompt Playground scenario %q purpose is required", id)
		}
		if len(scenario.CaseIDs) == 0 {
			return fmt.Errorf("Prompt Playground scenario %q must reference at least one case", id)
		}
	}
	return nil
}

func (m PromptPlaygroundManifest) ValidateAgainst(cases []EvalCase) error {
	if err := m.Validate(); err != nil {
		return err
	}
	caseIDs := make(map[string]struct{}, len(cases))
	for _, currentCase := range cases {
		caseIDs[currentCase.ID] = struct{}{}
	}
	mapped := make(map[string]string)
	for _, scenario := range m.Scenarios {
		for _, caseID := range scenario.CaseIDs {
			caseID = strings.TrimSpace(caseID)
			if _, exists := caseIDs[caseID]; !exists {
				return fmt.Errorf("Prompt Playground scenario %q references unknown case %q", scenario.ID, caseID)
			}
			if previous, exists := mapped[caseID]; exists {
				return fmt.Errorf("case %q is mapped by both %q and %q", caseID, previous, scenario.ID)
			}
			mapped[caseID] = scenario.ID
		}
	}
	if len(mapped) != len(caseIDs) {
		for caseID := range caseIDs {
			if _, exists := mapped[caseID]; !exists {
				return fmt.Errorf("case %q is not mapped to a Prompt Playground scenario", caseID)
			}
		}
	}
	return nil
}
