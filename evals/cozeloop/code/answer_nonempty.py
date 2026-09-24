def exec_evaluation(turn):
    try:
        actual_output = turn["evaluate_target_output_fields"]["actual_output"]["text"]
        if not isinstance(actual_output, str):
            raise ValueError("actual_output.text must be a string")

        passed = bool(actual_output.strip())
        score = 1.0 if passed else 0.0
        reason = "actual_output is non-empty" if passed else "actual_output is empty"
        return EvalOutput(score=score, reason=reason)
    except KeyError as error:
        raise ValueError(f"required evaluator field is missing: {error}")
