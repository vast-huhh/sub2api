package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type balanceCardCodeRepoStub struct {
	RedeemCodeRepository
	created []RedeemCode
	code    RedeemCode
}

func (r *balanceCardCodeRepoStub) Create(ctx context.Context, code *RedeemCode) error {
	r.code = *code
	return nil
}

func (r *balanceCardCodeRepoStub) CreateBatch(ctx context.Context, codes []RedeemCode) error {
	r.created = append([]RedeemCode(nil), codes...)
	for i := range codes {
		codes[i].ID = int64(i + 1)
	}
	return nil
}

func (r *balanceCardCodeRepoStub) GetByCode(ctx context.Context, code string) (*RedeemCode, error) {
	if r.code.Code != code {
		return nil, ErrRedeemCodeNotFound
	}
	result := r.code
	return &result, nil
}

func (r *balanceCardCodeRepoStub) GetByID(ctx context.Context, id int64) (*RedeemCode, error) {
	if r.code.ID != id {
		return nil, ErrRedeemCodeNotFound
	}
	result := r.code
	return &result, nil
}

type balanceCardIssueRepoStub struct {
	BalanceCardRepository
	plan         BalanceCardPlan
	redeemCalled bool
	redeemCodeID int64
	userID       int64
	planID       int64
	code         string
	onRedeem     func()
}

func (r *balanceCardIssueRepoStub) GetPlan(ctx context.Context, id int64) (*BalanceCardPlan, error) {
	if r.plan.ID != id {
		return nil, ErrBalanceCardPlanNotFound
	}
	plan := r.plan
	return &plan, nil
}

func (r *balanceCardIssueRepoStub) RedeemCard(ctx context.Context, redeemCodeID, userID, planID int64, code string, now time.Time) (*UserBalanceCard, error) {
	r.redeemCalled = true
	r.redeemCodeID = redeemCodeID
	r.userID = userID
	r.planID = planID
	r.code = code
	if r.onRedeem != nil {
		r.onRedeem()
	}
	return &UserBalanceCard{ID: 91, UserID: userID, PlanID: &planID, PlanName: r.plan.Name}, nil
}

type balanceCardRedeemUserRepoStub struct {
	UserRepository
	user User
}

func (r *balanceCardRedeemUserRepoStub) GetByID(ctx context.Context, id int64) (*User, error) {
	if r.user.ID != id {
		return nil, ErrUserNotFound
	}
	user := r.user
	return &user, nil
}

func TestGenerateBalanceCardCodesStoresPlanSnapshot(t *testing.T) {
	ctx := context.Background()
	codeRepo := &balanceCardCodeRepoStub{}
	cardRepo := &balanceCardIssueRepoStub{plan: BalanceCardPlan{
		ID: 7, Name: "Monthly 1000", Status: StatusActive,
	}}
	svc := NewRedeemService(codeRepo, nil, nil, nil, nil, nil, nil, nil)
	svc.SetBalanceCardService(NewBalanceCardService(cardRepo, nil))
	expiresAt := time.Now().Add(24 * time.Hour)

	codes, err := svc.GenerateBalanceCardCodes(ctx, 2, cardRepo.plan.ID, &expiresAt)

	require.NoError(t, err)
	require.Len(t, codes, 2)
	require.Len(t, codeRepo.created, 2)
	for _, code := range codes {
		require.NotEmpty(t, code.Code)
		require.Equal(t, RedeemTypeBalanceCard, code.Type)
		require.Equal(t, StatusUnused, code.Status)
		require.Equal(t, cardRepo.plan.ID, *code.BalanceCardPlanID)
		require.Equal(t, cardRepo.plan.Name, code.BalanceCardPlanName)
		require.Equal(t, expiresAt, *code.ExpiresAt)
	}
}

func TestRedeemBalanceCardDelegatesAtomicIssuance(t *testing.T) {
	ctx := context.Background()
	planID := int64(7)
	userID := int64(12)
	usedBy := userID
	codeRepo := &balanceCardCodeRepoStub{code: RedeemCode{
		ID: 33, Code: "CARD-CODE", Type: RedeemTypeBalanceCard, Status: StatusUnused,
		BalanceCardPlanID: &planID, BalanceCardPlanName: "Monthly 1000",
	}}
	cardRepo := &balanceCardIssueRepoStub{plan: BalanceCardPlan{
		ID: planID, Name: "Monthly 1000", Status: StatusActive,
	}}
	cardRepo.onRedeem = func() {
		codeRepo.code.Status = StatusUsed
		codeRepo.code.UsedBy = &usedBy
	}
	svc := NewRedeemService(codeRepo, &balanceCardRedeemUserRepoStub{user: User{ID: userID}}, nil, nil, nil, nil, nil, nil)
	svc.SetBalanceCardService(NewBalanceCardService(cardRepo, nil))

	result, err := svc.Redeem(ctx, userID, codeRepo.code.Code)

	require.NoError(t, err)
	require.Equal(t, StatusUsed, result.Status)
	require.Equal(t, userID, *result.UsedBy)
	require.True(t, cardRepo.redeemCalled)
	require.Equal(t, codeRepo.code.ID, cardRepo.redeemCodeID)
	require.Equal(t, userID, cardRepo.userID)
	require.Equal(t, planID, cardRepo.planID)
	require.Equal(t, codeRepo.code.Code, cardRepo.code)
}
