package controller

import (
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Setup struct {
	Status       bool   `json:"status"`
	RootInit     bool   `json:"root_init"`
	DatabaseType string `json:"database_type"`
}

type SetupRequest struct {
	Username           string `json:"username"`
	Password           string `json:"password"`
	ConfirmPassword    string `json:"confirmPassword"`
	SelfUseModeEnabled bool   `json:"SelfUseModeEnabled"`
	DemoSiteEnabled    bool   `json:"DemoSiteEnabled"`
}

var errSetupAlreadyCompleted = errors.New("setup already completed")
var setupMu sync.Mutex

func GetSetup(c *gin.Context) {
	setup := Setup{
		Status: constant.IsSetup(),
	}
	if setup.Status {
		c.JSON(200, gin.H{
			"success": true,
			"data":    setup,
		})
		return
	}
	setup.RootInit = model.RootUserExists()
	setup.DatabaseType = string(common.MainDatabaseType())
	c.JSON(200, gin.H{
		"success": true,
		"data":    setup,
	})
}

func PostSetup(c *gin.Context) {
	if constant.IsSetup() {
		setupDone(c)
		return
	}

	var req SetupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, gin.H{
			"success": false,
			"message": "请求参数有误",
		})
		return
	}

	// Setup is a singleton write. The process lock avoids duplicate password
	// hashing and noisy failed transactions; the database singleton remains the
	// cross-instance authority.
	setupMu.Lock()
	defer setupMu.Unlock()
	if constant.IsSetup() {
		setupDone(c)
		return
	}

	rootExists := model.RootUserExists()

	var rootUser model.User
	if rootExists {
		if strings.TrimSpace(req.Username) == "" || req.Password == "" {
			c.JSON(200, gin.H{
				"success": false,
				"message": "请输入现有管理员账号以完成初始化",
			})
			return
		}
		var existingRoot model.User
		if err := model.DB.Where("role = ?", common.RoleRootUser).First(&existingRoot).Error; err != nil {
			c.JSON(200, gin.H{
				"success": false,
				"message": "系统初始化失败: 无法读取管理员账号",
			})
			return
		}
		if existingRoot.Username != req.Username || !common.ValidatePasswordAndHash(req.Password, existingRoot.Password) {
			c.JSON(200, gin.H{
				"success": false,
				"message": "管理员账号验证失败",
			})
			return
		}
	} else {
		// Validate username length: max 12 characters to align with model.User validation
		if len(req.Username) > 12 {
			c.JSON(200, gin.H{
				"success": false,
				"message": "用户名长度不能超过12个字符",
			})
			return
		}
		// Validate password
		if req.Password != req.ConfirmPassword {
			c.JSON(200, gin.H{
				"success": false,
				"message": "两次输入的密码不一致",
			})
			return
		}

		if len(req.Password) < 8 {
			c.JSON(200, gin.H{
				"success": false,
				"message": "密码长度至少为8个字符",
			})
			return
		}

		// Create root user
		hashedPassword, err := common.Password2Hash(req.Password)
		if err != nil {
			c.JSON(200, gin.H{
				"success": false,
				"message": "系统错误: " + err.Error(),
			})
			return
		}
		rootUser = model.User{
			Username:    req.Username,
			Password:    hashedPassword,
			Role:        common.RoleRootUser,
			Status:      common.UserStatusEnabled,
			DisplayName: "Root User",
			AccessToken: nil,
			Quota:       100000000,
		}
	}

	err := model.DB.Transaction(func(tx *gorm.DB) error {
		setup := model.Setup{
			ID:            model.SetupSingletonID,
			Version:       common.Version,
			InitializedAt: time.Now().Unix(),
		}
		if err := tx.Create(&setup).Error; err != nil {
			if isUniqueConstraintError(err) {
				return errSetupAlreadyCompleted
			}
			return err
		}

		var rootCount int64
		if err := tx.Model(&model.User{}).Where("role = ?", common.RoleRootUser).Count(&rootCount).Error; err != nil {
			return err
		}
		if rootCount == 0 {
			if rootExists {
				return errors.New("root user disappeared during setup")
			}
			if err := tx.Create(&rootUser).Error; err != nil {
				return err
			}
		}

		return saveSetupOptions(tx, req)
	})
	if err != nil {
		if errors.Is(err, errSetupAlreadyCompleted) {
			constant.SetSetup(true)
			setupDone(c)
			return
		}
		c.JSON(200, gin.H{
			"success": false,
			"message": "系统初始化失败: " + err.Error(),
		})
		return
	}

	applySetupOperationModes(req)
	constant.SetSetup(true)

	c.JSON(200, gin.H{
		"success": true,
		"message": "系统初始化成功",
	})
}

func setupDone(c *gin.Context) {
	c.JSON(200, gin.H{
		"success": false,
		"message": "系统已经初始化完成",
	})
}

func saveSetupOptions(tx *gorm.DB, req SetupRequest) error {
	values := map[string]string{
		"SelfUseModeEnabled": boolToString(req.SelfUseModeEnabled),
		"DemoSiteEnabled":    boolToString(req.DemoSiteEnabled),
	}
	for key, value := range values {
		option := model.Option{Key: key}
		if err := tx.FirstOrCreate(&option, model.Option{Key: key}).Error; err != nil {
			return err
		}
		option.Value = value
		if err := tx.Save(&option).Error; err != nil {
			return err
		}
	}
	return nil
}

func applySetupOperationModes(req SetupRequest) {
	operation_setting.SelfUseModeEnabled = req.SelfUseModeEnabled
	operation_setting.DemoSiteEnabled = req.DemoSiteEnabled

	common.OptionMapRWMutex.Lock()
	defer common.OptionMapRWMutex.Unlock()
	if common.OptionMap == nil {
		return
	}
	common.OptionMap["SelfUseModeEnabled"] = boolToString(req.SelfUseModeEnabled)
	common.OptionMap["DemoSiteEnabled"] = boolToString(req.DemoSiteEnabled)
}

func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique") ||
		strings.Contains(message, "duplicate")
}

func boolToString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
