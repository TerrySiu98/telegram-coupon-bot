package main

import (
    "database/sql"
    "fmt"
    "html/template"
    "log"
    "net/http"
    "os"
    "regexp"
    "strconv"
    "strings"
    "time"

    "github.com/gin-gonic/gin"
    tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
    "golang.org/x/crypto/bcrypt"
    "gorm.io/driver/sqlite"
    "gorm.io/gorm"
    _ "github.com/go-sql-driver/mysql"
    "github.com/golang-jwt/jwt"
)

// 数据模型
type Coupon struct {
    ID        uint      `gorm:"primaryKey"`
    Code      string    `gorm:"unique;not null"`
    IsUsed    bool      `gorm:"default:false"`
    CreatedAt time.Time
    UpdatedAt time.Time
}

type UserRecord struct {
    ID         uint      `gorm:"primaryKey"`
    TelegramID int64
    Username   string
    Nickname   string
    Email      string    `gorm:"not null"`
    CouponCode string    `gorm:"not null"`
    CouponID   uint
    Coupon     Coupon
    RedeemedAt time.Time `gorm:"not null"`
    InGroup    bool      `gorm:"default:false"`
    CreatedAt  time.Time
    UpdatedAt  time.Time
}

type Admin struct {
    ID           uint   `gorm:"primaryKey"`
    Username     string `gorm:"unique;not null"`
    PasswordHash string `gorm:"not null"`
    CreatedAt    time.Time
    UpdatedAt    time.Time
}

type KeywordTrigger struct {
    ID          uint      `gorm:"primaryKey"`
    Keyword     string    `gorm:"not null"`
    Response    string    `gorm:"not null"`
    IsActive    bool      `gorm:"default:true"`
    AutoDelete  bool      `gorm:"default:false"`
    DeleteDelay int       `gorm:"default:60"`
    CreatedAt   time.Time
    UpdatedAt   time.Time
}

type SystemConfig struct {
    ID          uint      `gorm:"primaryKey"`
    Key         string    `gorm:"unique;not null"`
    Value       string
    Description string
    CreatedAt   time.Time
    UpdatedAt   time.Time
}

// 全局变量
var (
    db                 *gorm.DB
    websiteDB          *sql.DB
    bot                *tgbotapi.BotAPI
    botRunning         bool
    botStarted         bool
    DaysBetweenClaims  int = 28
    processedMessages  = make(map[int]bool)
)

// 用户状态管理
type UserState struct {
    Step   string
    Email  string
    ChatID int64
}

var userStates = make(map[int64]*UserState)

// JWT Claims
type Claims struct {
    Username string `json:"username"`
    jwt.StandardClaims
}

// 初始化系统配置
func initSystemConfig() {
    configItems := []struct {
        Key          string
        DefaultValue string
        Description  string
    }{
        {"telegram_token", "", "Telegram机器人令牌"},
        {"official_group_id", "", "官方群组ID"},
        {"days_between_claims", "28", "优惠码领取间隔（天）"},
        {"website_db_host", "", "网站数据库主机"},
        {"website_db_port", "3306", "网站数据库端口"},
        {"website_db_name", "", "网站数据库名称"},
        {"website_db_user", "", "网站数据库用户名"},
        {"website_db_pass", "", "网站数据库密码"},
    }

    for _, item := range configItems {
        var existingConfig SystemConfig
        result := db.Where("key = ?", item.Key).First(&existingConfig)
        if result.Error != nil {
            newConfig := SystemConfig{
                Key:         item.Key,
                Value:       item.DefaultValue,
                Description: item.Description,
                CreatedAt:   time.Now(),
                UpdatedAt:   time.Now(),
            }
            db.Create(&newConfig)
        } else if existingConfig.Value == "" {
            existingConfig.Value = item.DefaultValue
            existingConfig.Description = item.Description
            existingConfig.UpdatedAt = time.Now()
            db.Save(&existingConfig)
        }
    }

    messageTemplates := []struct {
        Key          string
        DefaultValue string
        Description  string
    }{
        {
            "message_welcome",
            "欢迎使用NetAccelera！\n加入官方群组 @NetAccelera\n当前可用优惠码数量：%d\n请输入您的注册邮箱以获取优惠码\n请确保您输入的邮箱已在我们的网站注册。\n每位用户每%d天只能领取一次优惠码。\n\n您可以随时发送 /cancel 取消操作。",
            "欢迎消息 (%d 为可用优惠码数量, %d 为领取间隔天数)",
        },
        {
            "message_email_invalid",
            "邮箱格式不正确，请重新输入有效的邮箱地址：\n或发送 /cancel 取消操作",
            "邮箱格式错误提示",
        },
        {
            "message_already_claimed",
            "您在%d天内已领取过优惠码，请在 %s 之后再来领取。",
            "已领取过优惠码提示 (%d 为领取间隔天数, %s 为下次可领取日期)",
        },
        {
            "message_email_not_registered",
            "抱歉，您提供的邮箱未在我们的网站注册。请重新输入已注册的邮箱：\n或发送 /cancel 取消操作",
            "邮箱未注册提示",
        },
        {
            "message_no_coupons",
            "抱歉，当前没有可用的优惠码了，请稍后再试。",
            "无可用优惠码提示",
        },
        {
            "message_success",
            "恭喜！您的优惠码是：`%s`\n请复制此优惠码并在网站结账时使用。\n\n您下次可在 %d 天后（%s 之后）再次领取。",
            "优惠码发放成功消息 (%s 为优惠码, %d 为领取间隔天数, %s 为下次可领取日期)",
        },
        {
            "message_not_in_group",
            "请先加入官方群组 @NetAccelera 后再尝试领取优惠码。",
            "用户不在群组提示",
        },
        {
            "message_start_command",
            "请输入 /start 开始使用机器人",
            "开始命令提示",
        },
        {
            "message_cancel",
            "已取消操作。输入 /start 重新开始。",
            "取消操作提示",
        },
    }

    for _, template := range messageTemplates {
        var existingConfig SystemConfig
        result := db.Where("key = ?", template.Key).First(&existingConfig)
        if result.Error != nil {
            newConfig := SystemConfig{
                Key:         template.Key,
                Value:       template.DefaultValue,
                Description: template.Description,
                CreatedAt:   time.Now(),
                UpdatedAt:   time.Now(),
            }
            db.Create(&newConfig)
        } else if existingConfig.Value == "" {
            existingConfig.Value = template.DefaultValue
            existingConfig.Description = template.Description
            existingConfig.UpdatedAt = time.Now()
            db.Save(&existingConfig)
        }
    }
}

// 从数据库加载系统配置
func loadSystemConfig() {
    var daysConfig SystemConfig
    if err := db.Where("key = ?", "days_between_claims").First(&daysConfig).Error; err != nil {
        log.Printf("加载 days_between_claims 失败: %v, 使用默认值 28", err)
        DaysBetweenClaims = 28
    } else if daysConfig.Value == "" {
        log.Println("days_between_claims 值为空，使用默认值 28")
        DaysBetweenClaims = 28
    } else {
        if days, err := strconv.Atoi(daysConfig.Value); err == nil && days > 0 {
            DaysBetweenClaims = days
            log.Printf("成功加载 days_between_claims: %d", DaysBetweenClaims)
        } else {
            log.Printf("days_between_claims 值无效: %s, 使用默认值 28", daysConfig.Value)
            DaysBetweenClaims = 28
        }
    }
}

// 检查用户是否可以领取优惠码
func canUserClaimCoupon(telegramID int64) (bool, time.Time) {
    var lastRecord UserRecord
    result := db.Where("telegram_id = ?", telegramID).Order("redeemed_at desc").First(&lastRecord)
    if result.Error != nil {
        return true, time.Time{}
    }
    nextAvailableTime := lastRecord.RedeemedAt.AddDate(0, 0, DaysBetweenClaims)
    return time.Now().After(nextAvailableTime), nextAvailableTime
}

// 获取下次可领取优惠码的时间格式化字符串
func getNextAvailableTimeString(nextTime time.Time) string {
    return nextTime.Format("2006年01月02日")
}

// 检查邮箱是否已在网站数据库中注册
func isEmailRegistered(email string) bool {
    if websiteDB == nil {
        log.Println("网站数据库连接未初始化")
        return false
    }
    query := "SELECT COUNT(*) FROM v2_user WHERE email = ?"
    var count int
    err := websiteDB.QueryRow(query, email).Scan(&count)
    if err != nil {
        log.Printf("查询邮箱注册状态失败: %v", err)
        return false
    }
    return count > 0
}

// 初始化网站数据库连接
func initWebsiteDB() {
    var dbHostConfig, dbPortConfig, dbNameConfig, dbUserConfig, dbPassConfig SystemConfig
    if db.Where("key = ?", "website_db_host").First(&dbHostConfig).Error != nil ||
        db.Where("key = ?", "website_db_name").First(&dbNameConfig).Error != nil ||
        db.Where("key = ?", "website_db_user").First(&dbUserConfig).Error != nil {
        log.Println("数据库配置不完整，无法连接")
        return
    }
    db.Where("key = ?", "website_db_port").First(&dbPortConfig)
    db.Where("key = ?", "website_db_pass").First(&dbPassConfig)

    port := dbPortConfig.Value
    if port == "" {
        port = "3306"
    }

    dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
        dbUserConfig.Value, dbPassConfig.Value, dbHostConfig.Value, port, dbNameConfig.Value)

    var err error
    websiteDB, err = sql.Open("mysql", dsn)
    if err != nil {
        log.Printf("连接网站数据库失败: %v", err)
        return
    }

    websiteDB.SetMaxOpenConns(10)
    websiteDB.SetMaxIdleConns(5)
    websiteDB.SetConnMaxLifetime(time.Minute * 5)

    err = websiteDB.Ping()
    if err != nil {
        log.Printf("网站数据库连接测试失败: %v", err)
        websiteDB = nil
        return
    }

    log.Println("成功连接到网站数据库")
}

// 初始化Telegram机器人
func initTelegramBot() {
    var tokenConfig SystemConfig
    if db.Where("key = ?", "telegram_token").First(&tokenConfig).Error != nil || tokenConfig.Value == "" {
        log.Println("Telegram令牌未设置，无法初始化机器人")
        return
    }

    var err error
    bot, err = tgbotapi.NewBotAPI(tokenConfig.Value)
    if err != nil {
        log.Printf("无法初始化Telegram机器人: %v", err)
        return
    }
    log.Printf("已授权账号 %s", bot.Self.UserName)
}

// 检查是否已存在管理员账户
func checkAdminExists() bool {
    var adminCount int64
    db.Model(&Admin{}).Count(&adminCount)
    return adminCount > 0
}

// 初始化管理员账户
func initAdmin(username, password string) error {
    if username == "" || password == "" {
        return fmt.Errorf("用户名和密码不能为空")
    }

    hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
    if err != nil {
        return fmt.Errorf("创建管理员失败: %v", err)
    }

    admin := Admin{
        Username:     username,
        PasswordHash: string(hashedPassword),
    }

    result := db.Create(&admin)
    if result.Error != nil {
        return fmt.Errorf("保存管理员账户失败: %v", result.Error)
    }

    return nil
}

// 初始化函数
func init() {
    os.MkdirAll("data", 0755)
    os.MkdirAll("templates", 0755)
    os.MkdirAll("static", 0755)

    var err error
    db, err = gorm.Open(sqlite.Open("data/coupon_bot.db"), &gorm.Config{})
    if err != nil {
        log.Fatal("无法连接本地数据库:", err)
    }

    db.AutoMigrate(&Coupon{}, &UserRecord{}, &Admin{}, &KeywordTrigger{}, &SystemConfig{})
}

// 主函数
func main() {
    initSystemConfig()
    loadSystemConfig()
    initWebsiteDB()
    initTelegramBot()

    // 检查并创建默认管理员账户
    if !checkAdminExists() {
        defaultUsername := "admin"
        defaultPassword := "admin"
        err := initAdmin(defaultUsername, defaultPassword)
        if err != nil {
            log.Fatalf("初始化管理员账户失败: %v", err)
        }
        log.Printf("管理员账户已创建，用户名: %s, 密码: %s（请在首次登录后修改）", defaultUsername, defaultPassword)
    }

    loc, err := time.LoadLocation("Asia/Shanghai")
    if err != nil {
        log.Printf("无法设置时区: %v，将使用系统默认时区", err)
    } else {
        time.Local = loc
    }

    if bot != nil {
        go startBot()
    } else {
        log.Println("Telegram机器人未初始化，将不会启动机器人服务")
    }

    startWebServer()
}

// 验证邮箱格式
func isValidEmail(email string) bool {
    re := regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)
    isValid := re.MatchString(email)
    if !isValid {
        log.Printf("邮箱格式验证失败: %s", email)
    }
    return isValid
}

// 启动机器人
func startBot() {
    if bot == nil || botStarted {
        log.Println("机器人未初始化或已启动，无法再次启动")
        return
    }

    botStarted = true
    botRunning = true
    log.Println("startBot 已启动")

    u := tgbotapi.NewUpdate(0)
    u.Timeout = 60

    updates := bot.GetUpdatesChan(u)

    for update := range updates {
        if update.Message != nil {
            msgID := update.Message.MessageID
            if processedMessages[msgID] {
                continue
            }
            processedMessages[msgID] = true

            handleMessage(update.Message)

            if len(processedMessages) > 1000 {
                for k := range processedMessages {
                    delete(processedMessages, k)
                    break
                }
            }

            u.Offset = update.UpdateID + 1
        }
    }

    botRunning = false
    botStarted = false
    log.Println("startBot 已退出")
}

// 检查用户是否在特定群组中
func isUserInGroup(userID int64) bool {
    var groupConfig SystemConfig
    result := db.Where("key = ?", "official_group_id").First(&groupConfig)
    if result.Error != nil {
        log.Printf("获取群组ID失败: %v", result.Error)
        return false
    }

    if groupConfig.Value == "" {
        log.Println("群组ID未设置")
        return false
    }

    groupID, err := strconv.ParseInt(groupConfig.Value, 10, 64)
    if err != nil {
        log.Printf("群组ID格式错误: %v", err)
        return false
    }

    chatMember, err := bot.GetChatMember(tgbotapi.GetChatMemberConfig{
        ChatConfigWithUser: tgbotapi.ChatConfigWithUser{
            ChatID: groupID,
            UserID: int64(userID),
        },
    })
    if err != nil {
        log.Printf("检查用户群组状态失败: %v", err)
        return false
    }

    return chatMember.Status == "member" ||
        chatMember.Status == "administrator" ||
        chatMember.Status == "creator"
}

// 检查并处理关键词触发
func checkAndHandleKeywordTrigger(message *tgbotapi.Message) bool {
    if message.Chat.Type != "group" && message.Chat.Type != "supergroup" {
        return false
    }

    if message.Text == "" {
        return false
    }

    var triggers []KeywordTrigger
    db.Where("is_active = ?", true).Find(&triggers)

    for _, trigger := range triggers {
        if strings.Contains(strings.ToLower(message.Text), strings.ToLower(trigger.Keyword)) {
            msg := tgbotapi.NewMessage(message.Chat.ID, trigger.Response)
            msg.ReplyToMessageID = message.MessageID

            sentMsg, err := bot.Send(msg)
            if err != nil {
                log.Printf("发送消息失败: %v", err)
                return true
            }

            if trigger.AutoDelete {
                go func(chatID int64, messageID int, replyToID int, delay int) {
                    time.Sleep(time.Duration(delay) * time.Second)
                    deleteMsg := tgbotapi.NewDeleteMessage(chatID, messageID)
                    _, err := bot.Request(deleteMsg)
                    if err != nil {
                        log.Printf("删除机器人消息失败: %v", err)
                    }
                    deleteOrigMsg := tgbotapi.NewDeleteMessage(chatID, replyToID)
                    _, err = bot.Request(deleteOrigMsg)
                    if err != nil {
                        log.Printf("删除用户消息失败: %v", err)
                    }
                }(sentMsg.Chat.ID, sentMsg.MessageID, message.MessageID, trigger.DeleteDelay)
            }

            return true
        }
    }

    return false
}

// 获取消息模板
func getMessageTemplate(key string) string {
    var msgConfig SystemConfig
    result := db.Where("key = ?", key).First(&msgConfig)
    if result.Error != nil {
        switch key {
        case "message_welcome":
            return "欢迎使用NetAccelera！\n加入官方群组 @NetAccelera\n当前可用优惠码数量：%d\n请输入您的注册邮箱以获取优惠码\n请确保您输入的邮箱已在我们的网站注册。\n每位用户每%d天只能领取一次优惠码。\n\n您可以随时发送 /cancel 取消操作。"
        case "message_email_invalid":
            return "邮箱格式不正确，请重新输入有效的邮箱地址：\n或发送 /cancel 取消操作"
        case "message_already_claimed":
            return "您在%d天内已领取过优惠码，请在 %s 之后再来领取。"
        case "message_email_not_registered":
            return "抱歉，您提供的邮箱未在我们的网站注册。请重新输入已注册的邮箱：\n或发送 /cancel 取消操作"
        case "message_no_coupons":
            return "抱歉，当前没有可用的优惠码了，请稍后再试。"
        case "message_success":
            return "恭喜！您的优惠码是：`%s`\n请复制此优惠码并在网站结账时使用。\n\n您下次可在 %d 天后（%s 之后）再次领取。"
        case "message_not_in_group":
            return "请先加入官方群组 @NetAccelera 后再尝试领取优惠码。"
        case "message_start_command":
            return "请输入 /start 开始使用机器人"
        case "message_cancel":
            return "已取消操作。输入 /start 重新开始。"
        default:
            return ""
        }
    }
    return msgConfig.Value
}

// 处理消息
func handleMessage(message *tgbotapi.Message) {
    userID := message.From.ID
    chatID := message.Chat.ID
    isGroup := message.Chat.Type == "group" || message.Chat.Type == "supergroup"

    if isGroup && (message.Text == "/start" || strings.ToLower(message.Text) == "start") {
        return
    }

    if checkAndHandleKeywordTrigger(message) {
        return
    }

    if isGroup {
        return
    }

    state, exists := userStates[int64(userID)]
    if !exists {
        state = &UserState{
            Step:   "initial",
            ChatID: chatID,
        }
        userStates[int64(userID)] = state
    }

    switch state.Step {
    case "initial":
        if message.Text == "/start" || strings.ToLower(message.Text) == "start" {
            var availableCouponCount int64
            db.Model(&Coupon{}).Where("is_used = ?", false).Count(&availableCouponCount)

            welcomeTemplate := getMessageTemplate("message_welcome")
            msg := tgbotapi.NewMessage(chatID, fmt.Sprintf(
                welcomeTemplate,
                availableCouponCount, DaysBetweenClaims))
            bot.Send(msg)

            state.Step = "wait_email"
        } else {
            startCommandMsg := getMessageTemplate("message_start_command")
            msg := tgbotapi.NewMessage(chatID, startCommandMsg)
            bot.Send(msg)
        }

    case "wait_email":
        email := strings.TrimSpace(message.Text)

        if strings.ToLower(email) == "/cancel" || strings.ToLower(email) == "cancel" {
            cancelMsg := getMessageTemplate("message_cancel")
            msg := tgbotapi.NewMessage(chatID, cancelMsg)
            bot.Send(msg)
            state.Step = "initial"
            return
        }

        if !isValidEmail(email) {
            invalidEmailMsg := getMessageTemplate("message_email_invalid")
            msg := tgbotapi.NewMessage(chatID, invalidEmailMsg)
            bot.Send(msg)
            return
        }

        canClaim, nextTime := canUserClaimCoupon(int64(userID))
        if !canClaim {
            nextTimeStr := getNextAvailableTimeString(nextTime)
            alreadyClaimedMsg := getMessageTemplate("message_already_claimed")
            msg := tgbotapi.NewMessage(chatID, fmt.Sprintf(alreadyClaimedMsg, DaysBetweenClaims, nextTimeStr))
            bot.Send(msg)
            state.Step = "initial"
            return
        }

        inGroup := isUserInGroup(int64(userID))
        if !inGroup {
            notInGroupMsg := getMessageTemplate("message_not_in_group") + "\n\n您可以发送 /cancel 取消操作。"
            msg := tgbotapi.NewMessage(chatID, notInGroupMsg)
            bot.Send(msg)
            state.Step = "initial"
            return
        }

        if !isEmailRegistered(email) {
            emailNotRegisteredMsg := getMessageTemplate("message_email_not_registered")
            msg := tgbotapi.NewMessage(chatID, emailNotRegisteredMsg)
            bot.Send(msg)
            return
        }

        state.Email = email

        var coupon Coupon
        result := db.Where("is_used = ?", false).First(&coupon)
        if result.Error != nil {
            noCouponsMsg := getMessageTemplate("message_no_coupons")
            msg := tgbotapi.NewMessage(chatID, noCouponsMsg)
            bot.Send(msg)
            state.Step = "initial"
            return
        }

        coupon.IsUsed = true
        db.Save(&coupon)

        userRecord := UserRecord{
            TelegramID: int64(userID),
            Username:   message.From.UserName,
            Nickname:   message.From.FirstName + " " + message.From.LastName,
            Email:      email,
            CouponCode: coupon.Code,
            CouponID:   coupon.ID,
            RedeemedAt: time.Now(),
            InGroup:    inGroup,
        }
        db.Create(&userRecord)

        successMsg := getMessageTemplate("message_success")
        msg := tgbotapi.NewMessage(chatID, fmt.Sprintf(successMsg,
            coupon.Code,
            DaysBetweenClaims,
            getNextAvailableTimeString(time.Now().AddDate(0, 0, DaysBetweenClaims))))
        msg.ParseMode = "Markdown"
        bot.Send(msg)

        state.Step = "initial"
    }
}

// 生成JWT令牌
func generateToken(username string) (string, error) {
    expirationTime := time.Now().Add(24 * time.Hour)
    claims := &Claims{
        Username: username,
        StandardClaims: jwt.StandardClaims{
            ExpiresAt: expirationTime.Unix(),
        },
    }

    token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
    return token.SignedString([]byte("your_jwt_secret_key"))
}

// 验证JWT令牌
func validateToken(tokenString string) (*Claims, error) {
    claims := &Claims{}

    token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
        return []byte("your_jwt_secret_key"), nil
    })

    if err != nil || !token.Valid {
        return nil, err
    }

    return claims, nil
}

// 身份验证中间件
func authMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        if c.Request.URL.Path == "/login" || strings.HasPrefix(c.Request.URL.Path, "/static/") {
            c.Next()
            return
        }

        tokenString := c.GetHeader("Authorization")
        if tokenString == "" {
            tokenCookie, err := c.Cookie("token")
            if err != nil {
                c.Redirect(http.StatusFound, "/login")
                c.Abort()
                return
            }
            tokenString = tokenCookie
        }

        claims, err := validateToken(tokenString)
        if err != nil {
            c.SetCookie("token", "", -1, "/", "", false, true)
            c.Redirect(http.StatusFound, "/login")
            c.Abort()
            return
        }

        c.Set("username", claims.Username)
        c.Next()
    }
}

// 启动Web服务器
func startWebServer() {
    r := gin.Default()
    r.Use(gin.Recovery())
    r.Use(authMiddleware())

    r.SetFuncMap(template.FuncMap{
        "add": func(a, b int) int {
            return a + b
        },
        "sub": func(a, b int) int {
            return a - b
        },
        "makeRange": func(min, max int) []int {
            a := make([]int, max-min+1)
            for i := range a {
                a[i] = min + i
            }
            return a
        },
        "pageRange": func(current, total int) []int {
            start := current - 5
            if start < 1 {
                start = 1
            }
            end := current + 5
            if end > total {
                end = total
            }
            a := make([]int, end-start+1)
            for i := range a {
                a[i] = start + i
            }
            return a
        },
        "formatTime": func(t time.Time) string {
            return t.Format("2006-01-02 15:04:05")
        },
        "canClaim": func(t time.Time) bool {
            nextTime := t.AddDate(0, 0, DaysBetweenClaims)
            return time.Now().After(nextTime)
        },
        "nextClaimTime": func(t time.Time) string {
            nextTime := t.AddDate(0, 0, DaysBetweenClaims)
            return nextTime.Format("2006-01-02")
        },
        "slice": func(s string, start, end int) string {
            if start < 0 || end > len(s) || start > end {
                return s
            }
            return s[start:end]
        },
    })

    r.LoadHTMLGlob("templates/*")
    r.Static("/static", "./static")

    r.GET("/login", func(c *gin.Context) {
        c.HTML(http.StatusOK, "login.html", gin.H{})
    })

    r.POST("/login", func(c *gin.Context) {
        username := c.PostForm("username")
        password := c.PostForm("password")

        var admin Admin
        result := db.Where("username = ?", username).First(&admin)
        if result.Error != nil {
            c.HTML(http.StatusOK, "login.html", gin.H{
                "error": "用户名或密码错误",
            })
            return
        }

        err := bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(password))
        if err != nil {
            c.HTML(http.StatusOK, "login.html", gin.H{
                "error": "用户名或密码错误",
            })
            return
        }

        token, err := generateToken(username)
        if err != nil {
            c.HTML(http.StatusOK, "login.html", gin.H{
                "error": "生成令牌失败",
            })
            return
        }

        c.SetCookie("token", token, int(24*time.Hour.Seconds()), "/", "", false, true)
        c.Redirect(http.StatusFound, "/")
    })

    r.GET("/", func(c *gin.Context) {
        username, _ := c.Get("username")

        var couponCount, usedCouponCount int64
        db.Model(&Coupon{}).Count(&couponCount)
        db.Model(&Coupon{}).Where("is_used = ?", true).Count(&usedCouponCount)

        var userCount int64
        db.Model(&UserRecord{}).Count(&userCount)

        var uniqueUserCount int64
        db.Model(&UserRecord{}).Distinct("telegram_id").Count(&uniqueUserCount)

        loc, _ := time.LoadLocation("Asia/Shanghai")
        now := time.Now().In(loc)
        year, month, day := now.Date()
        todayStart := time.Date(year, month, day, 0, 0, 0, 0, loc)
        todayEnd := time.Date(year, month, day, 23, 59, 59, 999999999, loc)

        var todayCount int64
        db.Model(&UserRecord{}).Where("redeemed_at BETWEEN ? AND ?", todayStart, todayEnd).Count(&todayCount)

        var keywordCount int64
        db.Model(&KeywordTrigger{}).Count(&keywordCount)

        log.Printf("仪表板状态 - bot: %v, botRunning: %v", bot != nil, botRunning)

        c.HTML(http.StatusOK, "dashboard.html", gin.H{
            "username":        username,
            "couponCount":     couponCount,
            "usedCouponCount": usedCouponCount,
            "availableCount":  couponCount - usedCouponCount,
            "userCount":       userCount,
            "uniqueUserCount": uniqueUserCount,
            "todayCount":      todayCount,
            "keywordCount":    keywordCount,
            "daysInterval":    DaysBetweenClaims,
            "botStatus":       bot != nil && botRunning,
            "dbStatus":        websiteDB != nil,
        })
    })

    r.POST("/refresh-status", func(c *gin.Context) {
        c.Redirect(http.StatusFound, "/")
    })

    r.GET("/coupons", func(c *gin.Context) {
        username, _ := c.Get("username")

        page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
        if page < 1 {
            page = 1
        }
        pageSize := 20
        offset := (page - 1) * pageSize

        filter := c.DefaultQuery("filter", "all")
        search := c.DefaultQuery("search", "")
        message := c.DefaultQuery("message", "")

        var coupons []Coupon
        query := db.Order("id desc")

        if filter == "used" {
            query = query.Where("is_used = ?", true)
        } else if filter == "available" {
            query = query.Where("is_used = ?", false)
        }

        if search != "" {
            query = query.Where("code LIKE ?", "%"+search+"%")
        }

        query = query.Offset(offset).Limit(pageSize)
        query.Find(&coupons)

        var total int64
        countQuery := db.Model(&Coupon{})
        if filter == "used" {
            countQuery = countQuery.Where("is_used = ?", true)
        } else if filter == "available" {
            countQuery = countQuery.Where("is_used = ?", false)
        }
        if search != "" {
            countQuery = countQuery.Where("code LIKE ?", "%"+search+"%")
        }
        countQuery.Count(&total)

        totalPages := (int(total) + pageSize - 1) / pageSize

        c.HTML(http.StatusOK, "coupons.html", gin.H{
            "username":   username,
            "coupons":    coupons,
            "page":       page,
            "totalPages": totalPages,
            "filter":     filter,
            "search":     search,
            "total":      total,
            "message":    message,
        })
    })

    r.POST("/coupons/add", func(c *gin.Context) {
        code := strings.TrimSpace(c.PostForm("code"))
        if code == "" {
            c.JSON(http.StatusBadRequest, gin.H{"error": "优惠码不能为空"})
            return
        }

        coupon := Coupon{
            Code:   code,
            IsUsed: false,
        }
        result := db.Create(&coupon)
        if result.Error != nil {
            c.JSON(http.StatusInternalServerError, gin.H{"error": "添加失败，可能是优惠码重复"})
            return
        }

        c.Redirect(http.StatusFound, "/coupons")
    })

    r.POST("/coupons/batch", func(c *gin.Context) {
        codes := c.PostForm("codes")
        codeList := strings.Split(codes, "\n")

        for _, code := range codeList {
            code = strings.TrimSpace(code)
            if code == "" {
                continue
            }

            coupon := Coupon{
                Code:   code,
                IsUsed: false,
            }
            db.Create(&coupon)
        }

        c.Redirect(http.StatusFound, "/coupons")
    })

    r.POST("/coupons/delete/:id", func(c *gin.Context) {
        id := c.Param("id")
        db.Delete(&Coupon{}, id)
        c.Redirect(http.StatusFound, "/coupons")
    })

    r.POST("/coupons/clear-used", func(c *gin.Context) {
        result := db.Where("is_used = ?", true).Delete(&Coupon{})
        if result.Error != nil {
            c.Redirect(http.StatusFound, "/coupons?message=清除失败："+result.Error.Error())
        } else {
            c.Redirect(http.StatusFound, "/coupons?message=成功清除"+strconv.FormatInt(result.RowsAffected, 10)+"个已使用的优惠码")
        }
    })

    r.GET("/users", func(c *gin.Context) {
        username, _ := c.Get("username")

        page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
        if page < 1 {
            page = 1
        }
        pageSize := 20
        offset := (page - 1) * pageSize

        search := c.DefaultQuery("search", "")
        message := c.DefaultQuery("message", "")

        var records []UserRecord
        query := db.Order("id desc").Offset(offset).Limit(pageSize)
        if search != "" {
            query = query.Where("email LIKE ? OR nickname LIKE ? OR username LIKE ? OR coupon_code LIKE ? OR telegram_id LIKE ?",
                "%"+search+"%", "%"+search+"%", "%"+search+"%", "%"+search+"%", "%"+search+"%")
        }
        query.Find(&records)

        var total int64
        countQuery := db.Model(&UserRecord{})
        if search != "" {
            countQuery = countQuery.Where("email LIKE ? OR nickname LIKE ? OR username LIKE ? OR coupon_code LIKE ? OR telegram_id LIKE ?",
                "%"+search+"%", "%"+search+"%", "%"+search+"%", "%"+search+"%", "%"+search+"%")
        }
        countQuery.Count(&total)

        totalPages := (int(total) + pageSize - 1) / pageSize

        c.HTML(http.StatusOK, "users.html", gin.H{
            "username":     username,
            "records":      records,
            "page":         page,
            "totalPages":   totalPages,
            "search":       search,
            "total":        total,
            "message":      message,
            "daysInterval": DaysBetweenClaims,
        })
    })

    r.POST("/users/delete/:id", func(c *gin.Context) {
        id := c.Param("id")
        var record UserRecord
        result := db.First(&record, id)
        if result.Error != nil {
            c.JSON(http.StatusNotFound, gin.H{"error": "记录不存在"})
            return
        }
        db.Delete(&record)
        c.Redirect(http.StatusFound, "/users")
    })

    r.GET("/configs", func(c *gin.Context) {
        username, _ := c.Get("username")

        var configs []SystemConfig
        db.Find(&configs)

        var telegramToken, officialGroupID, dbHost, dbPort, dbName, dbUser, dbPass string
        var otherConfigs []SystemConfig
        var messageTemplates []SystemConfig
        messageTemplatesMap := make(map[string]string)

        for _, cfg := range configs {
            if strings.HasPrefix(cfg.Key, "message_") {
                messageTemplates = append(messageTemplates, cfg)
                messageTemplatesMap[cfg.Key] = cfg.Value
                continue
            }

            switch cfg.Key {
            case "telegram_token":
                telegramToken = cfg.Value
            case "official_group_id":
                officialGroupID = cfg.Value
            case "website_db_host":
                dbHost = cfg.Value
            case "website_db_port":
                dbPort = cfg.Value
            case "website_db_name":
                dbName = cfg.Value
            case "website_db_user":
                dbUser = cfg.Value
            case "website_db_pass":
                dbPass = cfg.Value
            default:
                otherConfigs = append(otherConfigs, cfg)
            }
        }

        var keywords []KeywordTrigger
        db.Order("id desc").Find(&keywords)

        c.HTML(http.StatusOK, "configs.html", gin.H{
            "username":            username,
            "message":             c.DefaultQuery("message", ""),
            "telegramToken":       telegramToken,
            "officialGroupID":     officialGroupID,
            "daysInterval":        DaysBetweenClaims,
            "dbHost":              dbHost,
            "dbPort":              dbPort,
            "dbName":              dbName,
            "dbUser":              dbUser,
            "dbPass":              dbPass,
            "otherConfigs":        otherConfigs,
            "keywords":            keywords,
            "messageTemplates":    messageTemplates,
            "messageTemplatesMap": messageTemplatesMap,
        })
    })

    r.POST("/configs/update-telegram-group", func(c *gin.Context) {
        telegramToken := strings.TrimSpace(c.PostForm("telegram_token"))
        officialGroupID := strings.TrimSpace(c.PostForm("official_group_id"))

        if telegramToken != "" {
            var tokenConfig SystemConfig
            db.Where("key = ?", "telegram_token").First(&tokenConfig)
            tokenConfig.Value = telegramToken
            db.Save(&tokenConfig)
        }

        if officialGroupID != "" {
            var groupConfig SystemConfig
            db.Where("key = ?", "official_group_id").First(&groupConfig)
            groupConfig.Value = officialGroupID
            db.Save(&groupConfig)
        }

        c.Redirect(http.StatusFound, "/configs?message=Telegram和群组配置更新成功")
    })

    r.POST("/configs/update-db", func(c *gin.Context) {
        dbHost := strings.TrimSpace(c.PostForm("website_db_host"))
        dbPort := strings.TrimSpace(c.PostForm("website_db_port"))
        dbName := strings.TrimSpace(c.PostForm("website_db_name"))
        dbUser := strings.TrimSpace(c.PostForm("website_db_user"))
        dbPass := strings.TrimSpace(c.PostForm("website_db_pass"))

        if dbHost != "" {
            var config SystemConfig
            db.Where("key = ?", "website_db_host").First(&config)
            config.Value = dbHost
            db.Save(&config)
        }

        if dbPort != "" {
            var config SystemConfig
            db.Where("key = ?", "website_db_port").First(&config)
            config.Value = dbPort
            db.Save(&config)
        }

        if dbName != "" {
            var config SystemConfig
            db.Where("key = ?", "website_db_name").First(&config)
            config.Value = dbName
            db.Save(&config)
        }

        if dbUser != "" {
            var config SystemConfig
            db.Where("key = ?", "website_db_user").First(&config)
            config.Value = dbUser
            db.Save(&config)
        }

        if dbPass != "" {
            var config SystemConfig
            db.Where("key = ?", "website_db_pass").First(&config)
            config.Value = dbPass
            db.Save(&config)
        }

        c.Redirect(http.StatusFound, "/configs?message=数据库配置更新成功")
    })

    r.POST("/configs/update", func(c *gin.Context) {
        c.Redirect(http.StatusFound, "/configs?message=无效的配置ID")
    })

    r.POST("/configs/update/:id", func(c *gin.Context) {
        id := c.Param("id")
        value := strings.TrimSpace(c.PostForm("value"))

        var config SystemConfig
        result := db.First(&config, id)
        if result.Error != nil {
            log.Printf("未找到配置 ID %s: %v", id, result.Error)
            c.Redirect(http.StatusFound, "/configs?message=未找到该配置")
            return
        }

        config.Value = value
        config.UpdatedAt = time.Now()
        result = db.Save(&config)
        if result.Error != nil {
            log.Printf("保存配置失败 ID %s: %v", id, result.Error)
            c.Redirect(http.StatusFound, "/configs?message=保存失败："+result.Error.Error())
            return
        }

        loadSystemConfig()

        if config.Key == "telegram_token" && botRunning {
            botRunning = false
            bot = nil
            initTelegramBot()
            if bot != nil && !botStarted {
                go startBot()
            }
        }

        c.Redirect(http.StatusFound, "/configs?message=更新成功")
    })

    r.POST("/configs/add", func(c *gin.Context) {
        key := strings.TrimSpace(c.PostForm("key"))
        value := strings.TrimSpace(c.PostForm("value"))
        description := strings.TrimSpace(c.PostForm("description"))

        if key == "" || value == "" || description == "" {
            c.Redirect(http.StatusFound, "/configs?message=配置名、值和描述不能为空")
            return
        }

        config := SystemConfig{
            Key:         key,
            Value:       value,
            Description: description,
        }

        result := db.Create(&config)
        if result.Error != nil {
            c.Redirect(http.StatusFound, "/configs?message=添加失败："+result.Error.Error())
            return
        }

        c.Redirect(http.StatusFound, "/configs?message=添加成功")
    })

    r.POST("/configs/add-keyword", func(c *gin.Context) {
        keyword := strings.TrimSpace(c.PostForm("keyword"))
        response := strings.TrimSpace(c.PostForm("response"))
        autoDelete := c.PostForm("auto_delete") == "1"
        deleteDelay, _ := strconv.Atoi(c.PostForm("delete_delay"))

        if deleteDelay < 5 {
            deleteDelay = 5
        } else if deleteDelay > 300 {
            deleteDelay = 300
        }

        if keyword == "" || response == "" {
            c.Redirect(http.StatusFound, "/configs?message=关键词和回复内容不能为空")
            return
        }

        trigger := KeywordTrigger{
            Keyword:     keyword,
            Response:    response,
            IsActive:    true,
            AutoDelete:  autoDelete,
            DeleteDelay: deleteDelay,
        }
        db.Create(&trigger)

        c.Redirect(http.StatusFound, "/configs?message=添加成功")
    })

    r.POST("/configs/update-keyword/:id", func(c *gin.Context) {
        id := c.Param("id")
        keyword := strings.TrimSpace(c.PostForm("keyword"))
        response := strings.TrimSpace(c.PostForm("response"))
        isActive := c.PostForm("is_active") == "on"
        autoDelete := c.PostForm("auto_delete") == "on"
        deleteDelay, _ := strconv.Atoi(c.PostForm("delete_delay"))

        if deleteDelay < 5 {
            deleteDelay = 5
        } else if deleteDelay > 300 {
            deleteDelay = 300
        }

        if keyword == "" || response == "" {
            c.Redirect(http.StatusFound, "/configs?message=关键词和回复内容不能为空")
            return
        }

        var trigger KeywordTrigger
        db.First(&trigger, id)
        trigger.Keyword = keyword
        trigger.Response = response
        trigger.IsActive = isActive
        trigger.AutoDelete = autoDelete
        trigger.DeleteDelay = deleteDelay
        db.Save(&trigger)

        c.Redirect(http.StatusFound, "/configs?message=更新成功")
    })

    r.POST("/configs/delete-keyword/:id", func(c *gin.Context) {
        id := c.Param("id")
        db.Delete(&KeywordTrigger{}, id)
        c.Redirect(http.StatusFound, "/configs?message=删除成功")
    })

    r.POST("/configs/update-messages", func(c *gin.Context) {
        messageKeys := []string{
            "message_welcome",
            "message_email_invalid",
            "message_already_claimed",
            "message_email_not_registered",
            "message_no_coupons",
            "message_success",
            "message_not_in_group",
            "message_start_command",
            "message_cancel",
        }

        for _, key := range messageKeys {
            newValue := strings.TrimSpace(c.PostForm(key))
            if newValue != "" {
                var config SystemConfig
                db.Where("key = ?", key).First(&config)
                config.Value = newValue
                db.Save(&config)
            }
        }

        c.Redirect(http.StatusFound, "/configs?message=消息模板更新成功")
    })

    r.POST("/configs/update-admin", func(c *gin.Context) {
        currentUsername, exists := c.Get("username")
        if !exists {
            c.JSON(http.StatusUnauthorized, gin.H{"error": "未授权"})
            return
        }

        newUsername := c.PostForm("new_username")
        newPassword := c.PostForm("new_password")
        confirmPassword := c.PostForm("confirm_password")

        if newUsername == "" {
            c.Redirect(http.StatusFound, "/configs?message=用户名不能为空")
            return
        }

        if newPassword != "" && newPassword != confirmPassword {
            c.Redirect(http.StatusFound, "/configs?message=两次输入的密码不一致")
            return
        }

        var admin Admin
        result := db.Where("username = ?", currentUsername).First(&admin)
        if result.Error != nil {
            c.Redirect(http.StatusFound, "/configs?message=未找到管理员账户")
            return
        }

        if newUsername != currentUsername {
            var existingAdmin Admin
            if db.Where("username = ?", newUsername).First(&existingAdmin).Error == nil {
                c.Redirect(http.StatusFound, "/configs?message=用户名已存在")
                return
            }
            admin.Username = newUsername
        }

        if newPassword != "" {
            hashedPassword, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
            if err != nil {
                c.Redirect(http.StatusFound, "/configs?message=密码哈希失败")
                return
            }
            admin.PasswordHash = string(hashedPassword)
        }

        db.Save(&admin)

        token, err := generateToken(newUsername)
        if err != nil {
            c.Redirect(http.StatusFound, "/configs?message=更新失败：生成令牌失败")
            return
        }
        c.SetCookie("token", token, int(24*time.Hour.Seconds()), "/", "", false, true)

        c.Redirect(http.StatusFound, "/configs?message=管理员信息修改成功")
    })

    r.POST("/configs/test-bot", func(c *gin.Context) {
        var tokenConfig SystemConfig
        if db.Where("key = ?", "telegram_token").First(&tokenConfig).Error != nil || tokenConfig.Value == "" {
            c.JSON(http.StatusOK, gin.H{"message": "请先配置Telegram令牌"})
            return
        }

        testBot, err := tgbotapi.NewBotAPI(tokenConfig.Value)
        if err != nil {
            c.JSON(http.StatusOK, gin.H{"message": "测试失败: " + err.Error()})
            return
        }

        c.JSON(http.StatusOK, gin.H{"message": "连接成功，机器人用户名: @" + testBot.Self.UserName})
    })

    r.POST("/configs/test-db", func(c *gin.Context) {
        var dbHostConfig, dbPortConfig, dbNameConfig, dbUserConfig, dbPassConfig SystemConfig
        if db.Where("key = ?", "website_db_host").First(&dbHostConfig).Error != nil ||
            db.Where("key = ?", "website_db_name").First(&dbNameConfig).Error != nil ||
            db.Where("key = ?", "website_db_user").First(&dbUserConfig).Error != nil {
            c.JSON(http.StatusOK, gin.H{"message": "请先配置完整的数据库信息"})
            return
        }

        db.Where("key = ?", "website_db_port").First(&dbPortConfig)
        db.Where("key = ?", "website_db_pass").First(&dbPassConfig)

        port := dbPortConfig.Value
        if port == "" {
            port = "3306"
        }

        dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
            dbUserConfig.Value, dbPassConfig.Value, dbHostConfig.Value, port, dbNameConfig.Value)

        testDB, err := sql.Open("mysql", dsn)
        if err != nil {
            c.JSON(http.StatusOK, gin.H{"message": "测试失败: " + err.Error()})
            return
        }
        defer testDB.Close()

        err = testDB.Ping()
        if err != nil {
            c.JSON(http.StatusOK, gin.H{"message": "测试失败: " + err.Error()})
            return
        }

        c.JSON(http.StatusOK, gin.H{"message": "数据库连接测试成功"})
    })

    r.POST("/configs/reload", func(c *gin.Context) {
        if botRunning {
            botRunning = false
            log.Println("停止现有机器人实例")
            time.Sleep(1 * time.Second)
        }

        bot = nil
        botStarted = false

        loadSystemConfig()
        initWebsiteDB()

        initTelegramBot()

        if bot != nil {
            go startBot()
            log.Println("重新启动机器人")
            time.Sleep(1 * time.Second)

            if botRunning {
                c.JSON(http.StatusOK, gin.H{"message": "系统配置重载成功"})
                return
            }
        }

        errorMsg := "系统配置重载失败："
        if bot == nil {
            errorMsg += " Telegram机器人初始化失败"
        } else if !botRunning {
            errorMsg += " Telegram机器人启动失败"
        }
        if websiteDB == nil {
            errorMsg += " 数据库连接失败"
        }

        c.JSON(http.StatusOK, gin.H{"message": errorMsg})
    })

    r.GET("/logout", func(c *gin.Context) {
        c.SetCookie("token", "", -1, "/", "", false, true)
        c.Redirect(http.StatusFound, "/login")
    })

    r.Run(":5656")
}
