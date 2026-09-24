import json


def exec_evaluation(turn):
    try:
        tool_trace_text = turn["evaluate_dataset_fields"]["tool_trace"]["text"]
        if not isinstance(tool_trace_text, str):
            raise ValueError("tool_trace.text must be a JSON string")
        tool_trace = json.loads(tool_trace_text)
        if not isinstance(tool_trace, list):
            raise ValueError("tool_trace must be a JSON array")

        errors = []
        allowed_keys = {"name", "arguments", "error"}
        for index, call in enumerate(tool_trace):
            if not isinstance(call, dict):
                errors.append(f"item {index} is not an object")
                continue
            name = call.get("name")
            if not isinstance(name, str) or not name.strip():
                errors.append(f"item {index} has no valid name")
            if "arguments" in call and call["arguments"] is not None and not isinstance(call["arguments"], dict):
                errors.append(f"item {index} has invalid arguments")
            if "error" in call and not isinstance(call["error"], str):
                errors.append(f"item {index} has an invalid error field")
            if not set(call).issubset(allowed_keys):
                errors.append(f"item {index} has unexpected fields")

        passed = not errors
        score = 1.0 if passed else 0.0
        reason = "tool trace structure is valid" if passed else f"invalid tool trace item count: {len(errors)}"
        return EvalOutput(score=score, reason=reason)
    except KeyError as error:
        raise ValueError(f"required evaluator field is missing: {error}")
    except json.JSONDecodeError:
        raise ValueError("tool_trace must contain valid JSON")
