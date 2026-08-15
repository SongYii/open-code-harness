package application

import (
	"context"
	"fmt"
	"time"
)

type AppendResolutionConfig struct {
	Timeout       time.Duration
	MaxOperations uint32
}

func DefaultAppendResolutionConfig() AppendResolutionConfig {
	return AppendResolutionConfig{Timeout: DefaultAppendResolutionTimeout, MaxOperations: DefaultAppendResolutionMaxOperations}
}

func ResolveAppendIntent(ctx context.Context, store EventStoreV2, intent AppendIntent, config AppendResolutionConfig) (CommitReceipt, error) {
	if err := contextError(ctx); err != nil {
		return CommitReceipt{}, appendOutcomeUnknown(err)
	}
	if isNilValue(store) {
		return CommitReceipt{}, applicationError(CategoryValidation, "invalid_request", false, nil)
	}
	owned, err := cloneAppendIntent(intent)
	if err != nil {
		return CommitReceipt{}, storeContractViolation(err)
	}
	digest, err := DigestAppendRequest(owned.Request)
	if err != nil || digest != owned.Digest {
		if err != nil {
			return CommitReceipt{}, storeContractViolation(err)
		}
		return CommitReceipt{}, storeContractViolation(fmt.Errorf("intent digest mismatch"))
	}
	if config.MaxOperations == 0 {
		config.MaxOperations = DefaultAppendResolutionMaxOperations
	}
	resolveCtx := ctx
	cancel := func() {}
	if config.Timeout > 0 {
		resolveCtx, cancel = context.WithTimeout(ctx, config.Timeout)
	}
	defer cancel()

	var operations uint32
	for operations < config.MaxOperations {
		if err := contextError(resolveCtx); err != nil {
			return CommitReceipt{}, appendOutcomeUnknown(err)
		}
		operations++
		resolution, resolveErr := store.ResolveAppend(resolveCtx, ResolveAppendRequest{AppendID: owned.Request.AppendID, RequestDigest: owned.Digest})
		if !isNilValue(resolveErr) {
			if retryableResolutionUncertainty(resolveErr) {
				continue
			}
			if err := contextError(resolveCtx); err != nil {
				return CommitReceipt{}, appendOutcomeUnknown(err)
			}
			return CommitReceipt{}, mapV2StoreError(resolveCtx, resolveErr, "read")
		}
		if err := resolution.Validate(); err != nil {
			return CommitReceipt{}, storeContractViolation(err)
		}
		switch resolution.Kind {
		case AppendResolutionCommitted:
			if err := validateCommitReceipt(owned, *resolution.Receipt); err != nil {
				return CommitReceipt{}, storeContractViolation(err)
			}
			return *resolution.Receipt, nil
		case AppendResolutionIdentityMismatch:
			return CommitReceipt{}, applicationError(CategoryConflict, string(StoreCodeAppendIdentityMismatch), false, nil)
		case AppendResolutionNotFound:
			if operations >= config.MaxOperations {
				return CommitReceipt{}, appendOutcomeUnknown(nil)
			}
			if err := contextError(resolveCtx); err != nil {
				return CommitReceipt{}, appendOutcomeUnknown(err)
			}
			operations++
			request, cloneErr := cloneAppendIntent(owned)
			if cloneErr != nil {
				return CommitReceipt{}, storeContractViolation(cloneErr)
			}
			receipt, appendErr := store.Append(resolveCtx, request.Request)
			if !isNilValue(appendErr) {
				if retryableResolutionUncertainty(appendErr) {
					continue
				}
				if IsStoreCode(appendErr, StoreCodeAppendIdentityMismatch) {
					return CommitReceipt{}, applicationError(CategoryConflict, string(StoreCodeAppendIdentityMismatch), false, appendErr)
				}
				if err := contextError(resolveCtx); err != nil {
					return CommitReceipt{}, appendOutcomeUnknown(err)
				}
				return CommitReceipt{}, mapV2StoreError(resolveCtx, appendErr, "append")
			}
			if err := validateCommitReceipt(owned, receipt); err != nil {
				return CommitReceipt{}, storeContractViolation(err)
			}
			return receipt, nil
		default:
			return CommitReceipt{}, storeContractViolation(fmt.Errorf("unknown append resolution kind"))
		}
	}
	return CommitReceipt{}, appendOutcomeUnknown(nil)
}

func retryableResolutionUncertainty(err error) bool {
	return IsStoreCode(err, StoreCodeUnavailable) || IsStoreCode(err, StoreCodeCommitOutcomeUnknown)
}

func appendOutcomeUnknown(cause error) error {
	return applicationError(CategoryPersistence, "append_outcome_unknown", false, cause)
}
