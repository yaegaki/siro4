package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/labstack/echo/v4"
)

func scheduleHandler(c echo.Context) error {
	ctx := c.Request().Context()

	now := time.Now().In(jst)
	today := truncateHour(now)
	tommorow := today.Add(24 * time.Hour)
	client, err := createFirestoreClient(ctx)
	if err != nil {
		return c.String(http.StatusInternalServerError, "error")
	}
	schedule, err := getSchedule(ctx, client, today)
	if err != nil {
		return c.String(http.StatusInternalServerError, "error")
	}

	tommorowSchedule, err := getSchedule(ctx, client, tommorow)
	if err == nil {
		schedule = schedule.merge(tommorowSchedule)
	}

	schedule = schedule.getPart(now, 3*time.Hour)
	return c.JSON(http.StatusOK, schedule)
}

func exportHandler(c echo.Context) error {
	ctx := c.Request().Context()
	isDevelop := os.Getenv("DEVELOP") == "true"

	if !isDevelop && c.Request().Header.Get("X-Appengine-Cron") != "true" {
		return c.String(http.StatusBadRequest, "bad request")
	}

	log.Println("export task start")
	err := exportJob(ctx)
	if err != nil {
		return err
	}

	return c.String(http.StatusOK, "done.")
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	e := echo.New()
	e.GET("/schedule", scheduleHandler)
	e.GET("/_task/export", exportHandler)
	e.Static("/", "public")

	e.Logger.Fatal(e.Start(":" + port))
}
