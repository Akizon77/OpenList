package search

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
)

func validateIndexRequestRateLimit(value string) error {
	limit, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || math.IsNaN(limit) || math.IsInf(limit, 0) || limit < 0 {
		return fmt.Errorf("index request rate limit must be a non-negative number")
	}
	return nil
}

func init() {
	op.RegisterSettingItemHook(conf.IndexRequestRateLimit, func(item *model.SettingItem) error {
		return validateIndexRequestRateLimit(item.Value)
	})
}
