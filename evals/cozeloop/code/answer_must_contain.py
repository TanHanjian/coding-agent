import json


def exec_evaluation(turn):
    try:
        actual_output = turn["evaluate_target_output_fields"]["actual_output"]["text"]
        required_facts_text = turn["evaluate_dataset_fields"]["required_facts"]["text"]
        if not isinstance(actual_output, str):
            raise ValueError("actual_output.text must be a string")
        if not isinstance(required_facts_text, str):
            raise ValueError("required_facts.text must be a JSON string")
        required_facts = json.loads(required_facts_text)
        if not isinstance(required_facts, list) or any(not isinstance(fact, str) for fact in required_facts):
            raise ValueError("required_facts must be a JSON array of strings")

        missing_facts = [fact for fact in required_facts if fact.lower() not in actual_output.lower()]
        passed = not missing_facts
        score = 1.0 if passed else 0.0
        reason = "all required facts are present" if passed else f"missing required fact count: {len(missing_facts)}"
        return EvalOutput(score=score, reason=reason)
    except KeyError as error:
        raise ValueError(f"required evaluator field is missing: {error}")
    except json.JSONDecodeError:
        raise ValueError("required_facts must contain valid JSON")
