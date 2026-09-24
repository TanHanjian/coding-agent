import json


def exec_evaluation(turn):
    try:
        actual_output = turn["evaluate_target_output_fields"]["actual_output"]["text"]
        forbidden_content_text = turn["evaluate_dataset_fields"]["forbidden_content"]["text"]
        if not isinstance(actual_output, str):
            raise ValueError("actual_output.text must be a string")
        if not isinstance(forbidden_content_text, str):
            raise ValueError("forbidden_content.text must be a JSON string")
        forbidden_content = json.loads(forbidden_content_text)
        if not isinstance(forbidden_content, list) or any(not isinstance(item, str) for item in forbidden_content):
            raise ValueError("forbidden_content must be a JSON array of strings")

        matches = [item for item in forbidden_content if item.lower() in actual_output.lower()]
        passed = not matches
        score = 1.0 if passed else 0.0
        reason = "no forbidden content is present" if passed else f"forbidden content match count: {len(matches)}"
        return EvalOutput(score=score, reason=reason)
    except KeyError as error:
        raise ValueError(f"required evaluator field is missing: {error}")
    except json.JSONDecodeError:
        raise ValueError("forbidden_content must contain valid JSON")
