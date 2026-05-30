package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"taffy-server/internal/model"
	"taffy-server/internal/repository"
)

// SceneService 场景业务逻辑。
type SceneService struct {
	repo *repository.SceneRepo
}

func NewSceneService(repo *repository.SceneRepo) *SceneService {
	return &SceneService{repo: repo}
}

// CreateScene 创建场景。
func (s *SceneService) CreateScene(ctx context.Context, userID int64, name string, commandList json.RawMessage) (*model.Scene, error) {
	if name == "" {
		return nil, fmt.Errorf("场景名称不能为空")
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	scene := &model.Scene{
		UserID:      userID,
		Name:        name,
		CommandList: model.JSON(commandList),
	}
	id, err := s.repo.Create(cctx, scene)
	if err != nil {
		return nil, err
	}
	scene.SceneID = id
	return scene, nil
}

// GetScene 按 ID 查询场景。
func (s *SceneService) GetScene(ctx context.Context, id int64) (*model.Scene, error) {
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return s.repo.GetByID(cctx, id)
}

// ListScenes 查询用户所有场景。
func (s *SceneService) ListScenes(ctx context.Context, userID int64) ([]*model.Scene, error) {
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return s.repo.ListByUser(cctx, userID)
}

// UpdateScene 更新场景名称与指令列表。
func (s *SceneService) UpdateScene(ctx context.Context, userID, sceneID int64, name string, commandList json.RawMessage) error {
	if name == "" {
		return fmt.Errorf("场景名称不能为空")
	}
	scene, err := s.GetScene(ctx, sceneID)
	if err != nil {
		return err
	}
	if scene.UserID != userID {
		return fmt.Errorf("无权修改该场景")
	}

	scene.Name = name
	scene.CommandList = model.JSON(commandList)
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return s.repo.Update(cctx, scene)
}

// DeleteScene 删除场景（校验归属）。
func (s *SceneService) DeleteScene(ctx context.Context, userID, sceneID int64) error {
	scene, err := s.GetScene(ctx, sceneID)
	if err != nil {
		return err
	}
	if scene.UserID != userID {
		return fmt.Errorf("无权删除该场景")
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return s.repo.Delete(cctx, sceneID)
}

// Trigger 解析场景的 command_list，对每条指令调用 exec 执行。
// exec 是执行单条设备指令的函数（DeviceService.ExecuteControlDevice）。
func (s *SceneService) Trigger(ctx context.Context, sceneID int64, exec func(context.Context, json.RawMessage) DeviceCommandResult) ([]DeviceCommandResult, error) {
	scene, err := s.GetScene(ctx, sceneID)
	if err != nil {
		return nil, err
	}

	var commands []json.RawMessage
	if err := json.Unmarshal(scene.CommandList, &commands); err != nil {
		return nil, fmt.Errorf("解析场景指令失败: %w", err)
	}

	results := make([]DeviceCommandResult, 0, len(commands))
	for _, cmd := range commands {
		result := exec(ctx, cmd)
		results = append(results, result)
	}
	return results, nil
}
