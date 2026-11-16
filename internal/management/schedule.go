package management

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

func nextSchedule(schedule Schedule, after time.Time, timezone string) (time.Time, error) {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, invalid("系统时区无效")
	}
	if schedule.Kind == "interval" {
		if schedule.IntervalMinutes < 1 || schedule.IntervalMinutes > 525600 {
			return time.Time{}, invalid("固定周期必须为1到525600整数分钟")
		}
		return after.Add(time.Duration(schedule.IntervalMinutes) * time.Minute), nil
	}
	expression := schedule.Expression
	switch schedule.Kind {
	case "daily", "weekly":
		parts := strings.Split(schedule.TimeOfDay, ":")
		if len(parts) != 2 {
			return time.Time{}, invalid("每天或每周时间须为 HH:MM")
		}
		hour, first := strconv.Atoi(parts[0])
		minute, second := strconv.Atoi(parts[1])
		if first != nil || second != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
			return time.Time{}, invalid("每天或每周时间无效")
		}
		weekday := "*"
		if schedule.Kind == "weekly" {
			if schedule.Weekday < 0 || schedule.Weekday > 6 {
				return time.Time{}, invalid("每周日期必须为0（周日）到6（周六）")
			}
			weekday = strconv.Itoa(schedule.Weekday)
		}
		expression = fmt.Sprintf("%d %d * * %s", minute, hour, weekday)
	case "cron":
		if len(strings.Fields(expression)) != 5 {
			return time.Time{}, invalid("Cron 仅支持分、时、日、月、周五字段，最短1分钟")
		}
	default:
		return time.Time{}, invalid("计划类型必须为 interval、daily、weekly 或 cron")
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	parsed, err := parser.Parse(expression)
	if err != nil {
		return time.Time{}, invalid("Cron 无效，请检查五个字段及取值范围")
	}
	next := parsed.Next(after.In(location))
	if next.IsZero() {
		return time.Time{}, invalid("该计划在支持的日期范围内没有有效触发时间")
	}
	return next, nil
}

func scheduleChanged(first, second Schedule) bool {
	return first.Enabled != second.Enabled || first.Kind != second.Kind || first.Expression != second.Expression || first.IntervalMinutes != second.IntervalMinutes || first.TimeOfDay != second.TimeOfDay || first.Weekday != second.Weekday
}

func advanceSchedule(schedule Schedule, now time.Time, timezone string) (time.Time, error) {
	if schedule.Kind != "interval" || schedule.NextRunAt == nil {
		return nextSchedule(schedule, now, timezone)
	}
	period := time.Duration(schedule.IntervalMinutes) * time.Minute
	next := *schedule.NextRunAt
	if !next.After(now) {
		next = next.Add((now.Sub(next)/period + 1) * period)
	}
	return next, nil
}
