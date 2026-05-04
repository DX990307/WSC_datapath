package cp

import (
	"fmt"
	"math"
)

// 获取网格点的坐标
func getCoordinates(id int) (int, int) {
	if id == 0 {
		return 3, 3 // 中心点的坐标
	}
	row := (id - 1) / 7
	col := (id - 1) % 7
	return row, col
}

// 根据坐标返回网格点的ID
func getID(row, col int) int {
	if row == 3 && col == 3 {
		return 0 // 中心点
	}
	return row*7 + col + 1
}

// 判断是否为合法的ID
func isValid(row, col int) bool {
	return row >= 0 && row < 7 && col >= 0 && col < 7
}

// 获取下一步的ID
func getNextStep(a, b int) int {
	if a == b {
		return a // 已经到达目的地
	}

	// 获取起点和目标点的坐标
	startRow, startCol := getCoordinates(a)
	endRow, endCol := getCoordinates(b)

	// 计算移动方向
	var nextRow, nextCol int
	if math.Abs(float64(endRow-startRow)) > math.Abs(float64(endCol-startCol)) {
		// 优先垂直方向移动
		if endRow > startRow {
			nextRow = startRow + 1
		} else {
			nextRow = startRow - 1
		}
		nextCol = startCol
	} else {
		// 优先水平方向移动
		if endCol > startCol {
			nextCol = startCol + 1
		} else {
			nextCol = startCol - 1
		}
		nextRow = startRow
	}

	// 验证是否为合法ID
	if isValid(nextRow, nextCol) {
		return getID(nextRow, nextCol)
	}

	return -1 // 无效结果
}

func main() {
	// 示例输入
	a := 5  // 起点ID
	b := 32 // 目标点ID

	// 输出下一步的ID
	nextStep := getNextStep(a, b)
	fmt.Printf("从点 %d 到点 %d 的下一步是点 %d\n", a, b, nextStep)
}
