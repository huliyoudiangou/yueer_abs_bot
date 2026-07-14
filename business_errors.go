package main

import (
	"errors"
	"strings"
)

const unknownBusinessErrorCode = "UNKNOWN"

var (
	errLotteryNotActive               = errors.New("LOTTERY_NOT_ACTIVE")
	errLotteryWaitingDraw             = errors.New("LOTTERY_WAITING_DRAW")
	errLotteryFull                    = errors.New("LOTTERY_FULL")
	errLotteryAlreadyJoined           = errors.New("ALREADY_JOINED")
	errLotteryClaimExpired            = errors.New("LOTTERY_CLAIM_EXPIRED")
	errMarketplaceCloseNotFound       = errors.New("MARKETPLACE_CLOSE_NOT_FOUND")
	errSectSecretRealmNotActive       = errors.New("REALM_NOT_ACTIVE")
	errSectSecretRealmAlreadyActive   = errors.New("REALM_ALREADY_ACTIVE")
	errSectSecretRealmWeeklyLimit     = errors.New("REALM_WEEKLY_LIMIT")
	errSectTechnologyLevelChanged     = errors.New("TECH_LEVEL_CHANGED")
	errPointsNotEnough                = errors.New("POINTS_NOT_ENOUGH")
	errInsufficientPoints             = errors.New("INSUFFICIENT_POINTS")
	errAlreadyGrabbed                 = errors.New("ALREADY_GRABBED")
	errConcurrentRedPacketGrabRetry   = errors.New("CONCURRENT_REDPACKET_GRAB_RETRY")
	errAlreadyBet                     = errors.New("ALREADY_BET")
	errUsageLimitReached              = errors.New("USAGE_LIMIT_REACHED")
	errItemNotEnough                  = errors.New("ITEM_NOT_ENOUGH")
	errUserNotFound                   = errors.New("USER_NOT_FOUND")
	errSecurityPepperNotConfigured    = errors.New("SECURITY_PEPPER_NOT_CONFIGURED")
	errAlreadySigned                  = errors.New("ALREADY_SIGNED")
	errSignDateInFuture               = errors.New("SIGN_DATE_IN_FUTURE")
	errConcurrentSignInRetry          = errors.New("CONCURRENT_SIGN_IN_RETRY")
	errInvalidInviteCode              = errors.New("INVALID_INVITE_CODE")
	errInvalidRenewCode               = errors.New("INVALID_RENEW_CODE")
	errTelegramUserMissing            = errors.New("TELEGRAM_USER_MISSING")
	errAbsUserIDEmpty                 = errors.New("ABS_USER_ID_EMPTY")
	errUsernameTaken                  = errors.New("USERNAME_TAKEN")
	errUsernameUnchanged              = errors.New("USERNAME_UNCHANGED")
	errAbsRefreshFailed               = errors.New("ABS_REFRESH_FAILED")
	errAbsRefreshFailedUsingCache     = errors.New("ABS_REFRESH_FAILED_USING_CACHE")
	errDailyListeningStatsReadFailed  = errors.New("DAILY_LISTENING_STATS_READ_FAILED")
	errTargetIsSuperAdmin             = errors.New("target_is_super_admin")
	errAdjustNoEffect                 = errors.New("adjust_no_effect")
	errDailyAdjustLimitExceeded       = errors.New("daily_adjust_limit_exceeded")
	errAlreadyInSect                  = errors.New("ALREADY_IN_SECT")
	errSectNameExists                 = errors.New("SECT_NAME_EXISTS")
	errSectNotFound                   = errors.New("SECT_NOT_FOUND")
	errSectFull                       = errors.New("SECT_FULL")
	errNotInSect                      = errors.New("NOT_IN_SECT")
	errTargetNotInSect                = errors.New("TARGET_NOT_IN_SECT")
	errSectNoPermission               = errors.New("NO_PERMISSION")
	errSectSameName                   = errors.New("SAME_NAME")
	errSectFundsNotEnough             = errors.New("FUNDS_NOT_ENOUGH")
	errSectOnlyOwner                  = errors.New("ONLY_OWNER")
	errSectMaxLevel                   = errors.New("MAX_LEVEL")
	errSectPrestigeNotEnough          = errors.New("PRESTIGE_NOT_ENOUGH")
	errSectResourceNotEnough          = errors.New("RESOURCE_NOT_ENOUGH")
	errSectCannotAppointOwner         = errors.New("CANNOT_APPOINT_OWNER")
	errSectCaveLocked                 = errors.New("SECT_CAVE_LOCKED")
	errSectCaveAlreadyUnlocked        = errors.New("SECT_CAVE_ALREADY_UNLOCKED")
	errSectPersonalPrestigeNotEnough  = errors.New("PERSONAL_PRESTIGE_NOT_ENOUGH")
	errSectRetreatActive              = errors.New("SECT_RETREAT_ACTIVE")
	errSectRetreatNoEligibleMembers   = errors.New("SECT_RETREAT_NO_ELIGIBLE_MEMBERS")
	errSectShopRenewMonthlyLimit      = errors.New("SECT_SHOP_RENEW_MONTHLY_LIMIT")
	errSectShopRenewSectLimit         = errors.New("SECT_SHOP_RENEW_SECT_LIMIT")
	errSectShopRenewJoinedTooRecent   = errors.New("SECT_SHOP_RENEW_JOINED_TOO_RECENT")
	errSectShopRenewHistoryTooLow     = errors.New("SECT_SHOP_RENEW_HISTORY_TOO_LOW")
	errSectShopRenewExpireLimit       = errors.New("SECT_SHOP_RENEW_EXPIRE_LIMIT")
	errSectShopRenewPermanent         = errors.New("SECT_SHOP_RENEW_PERMANENT")
	errSectDailyTaskNotAllCompleted   = errors.New("SECT_DAILY_TASK_NOT_ALL_COMPLETED")
	errSectDailyTaskAlreadyClaimed    = errors.New("ALREADY_CLAIMED")
	errSectWeeklyTaskNotAchieved      = errors.New("SECT_WEEKLY_TASK_NOT_ACHIEVED")
	errSectWeeklyTaskAlreadySettled   = errors.New("SECT_WEEKLY_TASK_ALREADY_SETTLED")
	errSectLotteryContributionTooLow  = errors.New("SECT_LOTTERY_CONTRIBUTION_TOO_LOW")
	errSectLotteryUserIneligible      = errors.New("SECT_LOTTERY_USER_INELIGIBLE")
	errCultivationNotFound            = errors.New("CULTIVATION_NOT_FOUND")
	errMaxRealmReached                = errors.New("MAX_REALM_REACHED")
	errConsolidating                  = errors.New("CONSOLIDATING")
	errBreakthroughNotReady           = errors.New("NOT_READY")
	errInsufficientCultivation        = errors.New("INSUFFICIENT_CULTIVATION")
	errNoBreakthroughPill             = errors.New("NO_PILL")
	errInvalidBreakthroughMode        = errors.New("INVALID_BREAKTHROUGH_MODE")
	errCultivationStateChanged        = errors.New("CULTIVATION_STATE_CHANGED")
	errRandomFailed                   = errors.New("RANDOM_FAILED")
	errInvalidMarketplaceListing      = errors.New("INVALID_MARKETPLACE_LISTING")
	errMarketplaceDuplicateSecret     = errors.New("MARKETPLACE_DUPLICATE_SECRET")
	errMarketplaceInventoryNotEnough  = errors.New("MARKETPLACE_INVENTORY_NOT_ENOUGH")
	errMarketplaceListingNotFound     = errors.New("MARKETPLACE_LISTING_NOT_FOUND")
	errMarketplaceSelfBuy             = errors.New("MARKETPLACE_SELF_BUY")
	errMarketplaceOutOfStock          = errors.New("MARKETPLACE_OUT_OF_STOCK")
	errMarketplaceQuantityTooLarge    = errors.New("MARKETPLACE_QUANTITY_TOO_LARGE")
	errMarketplaceInvalidPrice        = errors.New("MARKETPLACE_INVALID_PRICE")
	errMarketplaceInvalidType         = errors.New("MARKETPLACE_INVALID_TYPE")
	errMarketplaceRealmTooLow         = errors.New("MARKETPLACE_REALM_TOO_LOW")
	errMarketplaceSellerMismatch      = errors.New("MARKETPLACE_SELLER_MISMATCH")
	errMarketplaceMixedSecretSource   = errors.New("MARKETPLACE_MIXED_SECRET_SOURCE")
	errMarketplaceVerifiedInvalid     = errors.New("MARKETPLACE_VERIFIED_SECRET_INVALID")
	errMarketplaceUnverifiedSecret    = errors.New("MARKETPLACE_UNVERIFIED_SECRET")
	errMarketplaceSecretOwnerMismatch = errors.New("MARKETPLACE_SECRET_OWNER_MISMATCH")
	errMarketplacePriceBelowFloor     = errors.New("MARKETPLACE_PRICE_BELOW_FLOOR")
	errMarketplacePriceAboveCeiling   = errors.New("MARKETPLACE_PRICE_ABOVE_CEILING")
	errCreateInviteCodeFailed         = errors.New("CREATE_INVITE_CODE_FAILED")
	errCreateRenewCodeFailed          = errors.New("CREATE_RENEW_CODE_FAILED")
	errRenewCodeOwnerMismatch         = errors.New("RENEW_CODE_OWNER_MISMATCH")
	errCreateRedPacketFailed          = errors.New("CREATE_REDPACKET_FAILED")
	errGardenPlotMax                  = errors.New("GARDEN_PLOT_MAX")
	errGardenDailyLimit               = errors.New("GARDEN_DAILY_LIMIT")
	errGardenSeedNotAvailable         = errors.New("GARDEN_SEED_NOT_AVAILABLE")
	errGardenSeedUnknown              = errors.New("GARDEN_SEED_UNKNOWN")
	errGardenPlotNotFound             = errors.New("GARDEN_PLOT_NOT_FOUND")
	errGardenPlotBusy                 = errors.New("GARDEN_PLOT_BUSY")
	errGardenNoEmptyPlot              = errors.New("GARDEN_NO_EMPTY_PLOT")
	errGardenSeedNotEnough            = errors.New("GARDEN_SEED_NOT_ENOUGH")
	errGardenNoActivePlant            = errors.New("GARDEN_NO_ACTIVE_PLANT")
	errGardenNotMature                = errors.New("GARDEN_NOT_MATURE")
	errGardenAlreadyHarvested         = errors.New("GARDEN_ALREADY_HARVESTED")
	errGardenNoMaturePlant            = errors.New("GARDEN_NO_MATURE_PLANT")
	errGardenHerbNotSellable          = errors.New("GARDEN_HERB_NOT_SELLABLE")
	errGardenHerbNotEnough            = errors.New("GARDEN_HERB_NOT_ENOUGH")
	errGardenHerbQuantityInvalid      = errors.New("GARDEN_HERB_QUANTITY_INVALID")
	errGardenRecipeUnknown            = errors.New("GARDEN_RECIPE_UNKNOWN")
	errGardenRecipeUnlocked           = errors.New("GARDEN_RECIPE_UNLOCKED")
	errGardenRecipeLocked             = errors.New("GARDEN_RECIPE_LOCKED")
	errGardenMaterialNotEnough        = errors.New("GARDEN_MATERIAL_NOT_ENOUGH")
)

func fallbackBusinessErrorCode(err error) string {
	if err == nil {
		return ""
	}

	code := strings.TrimSpace(err.Error())
	if isKnownBusinessErrorCode(code) {
		return code
	}
	return unknownBusinessErrorCode
}

func isKnownBusinessErrorCode(code string) bool {
	switch code {
	case errLotteryNotActive.Error(),
		errLotteryWaitingDraw.Error(),
		errLotteryFull.Error(),
		errLotteryAlreadyJoined.Error(),
		errLotteryClaimExpired.Error(),
		errMarketplaceCloseNotFound.Error(),
		errSectSecretRealmNotActive.Error(),
		errSectSecretRealmAlreadyActive.Error(),
		errSectSecretRealmWeeklyLimit.Error(),
		errSectTechnologyLevelChanged.Error(),
		errPointsNotEnough.Error(),
		errInsufficientPoints.Error(),
		errAlreadyGrabbed.Error(),
		errConcurrentRedPacketGrabRetry.Error(),
		errAlreadyBet.Error(),
		errUsageLimitReached.Error(),
		errItemNotEnough.Error(),
		errUserNotFound.Error(),
		errSecurityPepperNotConfigured.Error(),
		errAlreadySigned.Error(),
		errSignDateInFuture.Error(),
		errConcurrentSignInRetry.Error(),
		errInvalidInviteCode.Error(),
		errInvalidRenewCode.Error(),
		errTelegramUserMissing.Error(),
		errAbsUserIDEmpty.Error(),
		errAbsRefreshFailed.Error(),
		errAbsRefreshFailedUsingCache.Error(),
		errTargetIsSuperAdmin.Error(),
		errAdjustNoEffect.Error(),
		errDailyAdjustLimitExceeded.Error(),
		errAlreadyInSect.Error(),
		errSectNameExists.Error(),
		errSectNotFound.Error(),
		errSectFull.Error(),
		errNotInSect.Error(),
		errTargetNotInSect.Error(),
		errSectNoPermission.Error(),
		errSectSameName.Error(),
		errSectFundsNotEnough.Error(),
		errSectOnlyOwner.Error(),
		errSectMaxLevel.Error(),
		errSectPrestigeNotEnough.Error(),
		errSectResourceNotEnough.Error(),
		errSectCannotAppointOwner.Error(),
		errSectCaveLocked.Error(),
		errSectCaveAlreadyUnlocked.Error(),
		errSectPersonalPrestigeNotEnough.Error(),
		errSectRetreatActive.Error(),
		errSectRetreatNoEligibleMembers.Error(),
		errSectShopRenewMonthlyLimit.Error(),
		errSectShopRenewSectLimit.Error(),
		errSectShopRenewJoinedTooRecent.Error(),
		errSectShopRenewHistoryTooLow.Error(),
		errSectShopRenewExpireLimit.Error(),
		errSectShopRenewPermanent.Error(),
		errSectDailyTaskNotAllCompleted.Error(),
		errSectDailyTaskAlreadyClaimed.Error(),
		errSectWeeklyTaskNotAchieved.Error(),
		errSectWeeklyTaskAlreadySettled.Error(),
		errSectLotteryContributionTooLow.Error(),
		errSectLotteryUserIneligible.Error(),
		errCultivationNotFound.Error(),
		errMaxRealmReached.Error(),
		errConsolidating.Error(),
		errBreakthroughNotReady.Error(),
		errInsufficientCultivation.Error(),
		errNoBreakthroughPill.Error(),
		errInvalidBreakthroughMode.Error(),
		errCultivationStateChanged.Error(),
		errRandomFailed.Error(),
		errInvalidMarketplaceListing.Error(),
		errMarketplaceDuplicateSecret.Error(),
		errMarketplaceInventoryNotEnough.Error(),
		errMarketplaceListingNotFound.Error(),
		errMarketplaceSelfBuy.Error(),
		errMarketplaceOutOfStock.Error(),
		errMarketplaceQuantityTooLarge.Error(),
		errMarketplaceInvalidPrice.Error(),
		errMarketplaceInvalidType.Error(),
		errMarketplaceRealmTooLow.Error(),
		errMarketplaceSellerMismatch.Error(),
		errMarketplaceMixedSecretSource.Error(),
		errMarketplaceVerifiedInvalid.Error(),
		errMarketplaceUnverifiedSecret.Error(),
		errMarketplaceSecretOwnerMismatch.Error(),
		errMarketplacePriceBelowFloor.Error(),
		errMarketplacePriceAboveCeiling.Error(),
		errSectHornInvalidScope.Error(),
		errSectHornNoRecipients.Error(),
		errSectHornContentEmpty.Error(),
		errSectHornContentShort.Error(),
		errSectHornContentLong.Error(),
		errSectHornControlChar.Error(),
		errSectHornLinkBlocked.Error(),
		"SECT_HORN_COOLDOWN",
		errCreateInviteCodeFailed.Error(),
		errCreateRenewCodeFailed.Error(),
		errRenewCodeOwnerMismatch.Error(),
		errCreateRedPacketFailed.Error(),
		errGardenPlotMax.Error(),
		errGardenDailyLimit.Error(),
		errGardenSeedNotAvailable.Error(),
		errGardenSeedUnknown.Error(),
		errGardenPlotNotFound.Error(),
		errGardenPlotBusy.Error(),
		errGardenNoEmptyPlot.Error(),
		errGardenSeedNotEnough.Error(),
		errGardenNoActivePlant.Error(),
		errGardenNotMature.Error(),
		errGardenAlreadyHarvested.Error(),
		errGardenNoMaturePlant.Error(),
		errGardenHerbNotSellable.Error(),
		errGardenHerbNotEnough.Error(),
		errGardenHerbQuantityInvalid.Error(),
		errGardenRecipeUnknown.Error(),
		errGardenRecipeUnlocked.Error(),
		errGardenRecipeLocked.Error(),
		errGardenMaterialNotEnough.Error():
		return true
	default:
		return false
	}
}

func assetCreationErrorCode(err error) string {
	switch {
	case errors.Is(err, errUserNotFound):
		return "USER_NOT_FOUND"
	case errors.Is(err, errTrialCannotUseRenewCode):
		return "TRIAL_CANNOT_USE_RENEW_CODE"
	case errors.Is(err, errRenewCodeOwnerMismatch):
		return "RENEW_CODE_OWNER_MISMATCH"
	case errors.Is(err, errSecurityPepperNotConfigured):
		return "SECURITY_PEPPER_NOT_CONFIGURED"
	case errors.Is(err, errCreateInviteCodeFailed):
		return "CREATE_INVITE_CODE_FAILED"
	case errors.Is(err, errCreateRenewCodeFailed):
		return "CREATE_RENEW_CODE_FAILED"
	case errors.Is(err, errCreateRedPacketFailed):
		return "CREATE_REDPACKET_FAILED"
	case errors.Is(err, errInsufficientPoints):
		return "INSUFFICIENT_POINTS"
	case err != nil:
		return fallbackBusinessErrorCode(err)
	default:
		return ""
	}
}

func signInErrorCode(err error) string {
	switch {
	case errors.Is(err, errAlreadySigned):
		return "ALREADY_SIGNED"
	case errors.Is(err, errSignDateInFuture):
		return "SIGN_DATE_IN_FUTURE"
	case errors.Is(err, errConcurrentSignInRetry):
		return "CONCURRENT_SIGN_IN_RETRY"
	case err != nil:
		return fallbackBusinessErrorCode(err)
	default:
		return ""
	}
}

func renewRedeemErrorCode(err error) string {
	switch {
	case errors.Is(err, errInvalidRenewCode):
		return "INVALID_RENEW_CODE"
	case errors.Is(err, errUserNotFound):
		return "USER_NOT_FOUND"
	case errors.Is(err, errTrialCannotUseRenewCode):
		return "TRIAL_CANNOT_USE_RENEW_CODE"
	case errors.Is(err, errSecurityPepperNotConfigured):
		return "SECURITY_PEPPER_NOT_CONFIGURED"
	case err != nil:
		return fallbackBusinessErrorCode(err)
	default:
		return ""
	}
}
