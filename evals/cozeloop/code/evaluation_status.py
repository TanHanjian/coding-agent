def exec_evaluation(turn):
    try:
        status = turn["evaluate_dataset_fields"]["execution_status"]["text"]
        if not isinstance(status, str):
            raise ValueError("execution_status.text must be a string")

        passed = status.strip().lower() == "passed"
        score = 1.0 if passed else 0.0
        reason = f"local evaluation status is {status.strip() or '<empty>'}"
        return EvalOutput(score=score, reason=reason)
    except KeyError as error:
        raise ValueError(f"required evaluator field is missing: {error}")
