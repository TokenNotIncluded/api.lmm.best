package controller

import (
	"encoding/json"
	"io"
	"strconv"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

func GetMerchantStoreHome(c *gin.Context) {
	raw := c.Param("seller_id")
	id, err := strconv.Atoi(raw)
	if err != nil || raw != strconv.Itoa(id) || id < 1 || int64(id) > 2147483647 {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	home, err := model.GetMerchantStoreHome(merchantStoreViewer(c), id)
	merchantStoreRespond(c, home, err)
}

func GetMyMerchantStoreHome(c *gin.Context) {
	home, err := model.GetMerchantStoreHome(c.GetInt("id"), c.GetInt("id"))
	merchantStoreRespond(c, home, err)
}

func SaveMerchantStoreHome(c *gin.Context) {
	var input model.MerchantStoreHomeInput
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	home, err := model.SaveMerchantStoreHome(c.GetInt("id"), input)
	merchantStoreRespond(c, home, err)
}

func GetMerchantStoreAnnouncement(c *gin.Context) {
	content, err := model.GetMerchantStoreAnnouncement()
	merchantStoreRespond(c, gin.H{"content": content}, err)
}

func SaveMerchantStoreAnnouncement(c *gin.Context) {
	var input struct {
		Content string `json:"content"`
	}
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	err := model.SaveMerchantStoreAnnouncement(c.GetInt("id"), input.Content)
	merchantStoreRespond(c, gin.H{"content": input.Content}, err)
}
